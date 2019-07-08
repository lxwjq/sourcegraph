package repos

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"
	"github.com/sourcegraph/sourcegraph/pkg/api"
	"github.com/sourcegraph/sourcegraph/pkg/conf/reposource"
	"github.com/sourcegraph/sourcegraph/pkg/extsvc/bitbucketcloud"
	"github.com/sourcegraph/sourcegraph/pkg/httpcli"
	"github.com/sourcegraph/sourcegraph/pkg/jsonc"
	"github.com/sourcegraph/sourcegraph/schema"
	"gopkg.in/inconshreveable/log15.v2"
)

// A BitbucketCloudSource yields repositories from a single BitbucketCloud connection configured
// in Sourcegraph via the external services configuration.
type BitbucketCloudSource struct {
	svc             *ExternalService
	config          *schema.BitbucketCloudConnection
	exclude         map[string]bool
	excludePatterns []*regexp.Regexp
	client          *bitbucketcloud.Client
}

// NewBitbucketCloudSource returns a new BitbucketCloudSource from the given external service.
func NewBitbucketCloudSource(svc *ExternalService, cf *httpcli.Factory) (*BitbucketCloudSource, error) {
	var c schema.BitbucketCloudConnection
	if err := jsonc.Unmarshal(svc.Config, &c); err != nil {
		return nil, fmt.Errorf("external service id=%d config error: %s", svc.ID, err)
	}
	return newBitbucketCloudSource(svc, &c, cf)
}

func newBitbucketCloudSource(svc *ExternalService, c *schema.BitbucketCloudConnection, cf *httpcli.Factory) (*BitbucketCloudSource, error) {
	if cf == nil {
		cf = NewHTTPClientFactory()
	}

	opts := []httpcli.Opt{
		// Use a 30s timeout to avoid running into EOF errors
		httpcli.NewIdleConnTimeoutOpt(30 * time.Second),
	}

	cli, err := cf.Doer(opts...)
	if err != nil {
		return nil, err
	}

	exclude := make(map[string]bool, len(c.Exclude))
	var excludePatterns []*regexp.Regexp
	for _, r := range c.Exclude {
		if r.Name != "" {
			exclude[strings.ToLower(r.Name)] = true
		}

		if r.Id != 0 {
			exclude[strconv.Itoa(r.Id)] = true
		}

		if r.Pattern != "" {
			re, err := regexp.Compile(r.Pattern)
			if err != nil {
				return nil, err
			}
			excludePatterns = append(excludePatterns, re)
		}
	}

	client := bitbucketcloud.NewClient(cli)
	client.Username = c.Username
	client.AppPassword = c.AppPassword

	return &BitbucketCloudSource{
		svc:             svc,
		config:          c,
		exclude:         exclude,
		excludePatterns: excludePatterns,
		client:          client,
	}, nil
}

// ListRepos returns all Bitbucket Cloud repositories accessible to all connections configured
// in Sourcegraph via the external services configuration.
func (s BitbucketCloudSource) ListRepos(ctx context.Context) (repos []*Repo, err error) {
	rs, err := s.listAllRepos(ctx)
	for _, r := range rs {
		repos = append(repos, s.makeRepo(r))
	}
	return repos, err
}

// ExternalServices returns a singleton slice containing the external service.
func (s BitbucketCloudSource) ExternalServices() ExternalServices {
	return ExternalServices{s.svc}
}

func (s BitbucketCloudSource) makeRepo(r *bitbucketcloud.Repo) *Repo {
	host, err := url.Parse(s.config.Url)
	if err != nil {
		// This should never happen
		panic(errors.Errorf("malformed Bitbucket Cloud config, invalid URL: %q, error: %s", s.config.Url, err))
	}
	host = NormalizeBaseURL(host)

	urn := s.svc.URN()
	return &Repo{
		Name: string(reposource.BitbucketCloudRepoName(
			s.config.RepositoryPathPattern,
			host.Hostname(),
			r.FullName,
		)),
		URI: string(reposource.BitbucketCloudRepoName(
			"",
			host.Hostname(),
			r.FullName,
		)),
		ExternalRepo: api.ExternalRepoSpec{
			ID:          r.UUID,
			ServiceType: bitbucketcloud.ServiceType,
			ServiceID:   host.String(),
		},
		Description: r.Name,
		Fork:        r.Parent != nil,
		Enabled:     true,
		Sources: map[string]*SourceInfo{
			urn: {
				ID:       urn,
				CloneURL: s.authenticatedRemoteURL(r),
			},
		},
		Metadata: r,
	}
}

// authenticatedRemoteURL returns the repository's Git remote URL with the configured
// Bitbucket Cloud app password inserted in the URL userinfo.
func (s *BitbucketCloudSource) authenticatedRemoteURL(repo *bitbucketcloud.Repo) string {
	if s.config.GitURLType == "ssh" {
		url := fmt.Sprintf("git@%s:%s.git", s.config.Url, repo.FullName)
		return url
	}

	fallbackURL := (&url.URL{
		Scheme: "https",
		Host:   s.config.Url,
		Path:   "/" + repo.FullName,
	}).String()

	httpsURL, err := repo.Links.Clone.HTTPS()
	if err != nil {
		log15.Warn("Error adding authentication to Bitbucket Cloud repository Git remote URL.", "url", repo.Links.Clone, "error", err)
		return fallbackURL
	}
	u, err := url.Parse(httpsURL)
	if err != nil {
		log15.Warn("Error adding authentication to Bitbucket Cloud repository Git remote URL.", "url", httpsURL, "error", err)
		return fallbackURL
	}

	u.User = url.UserPassword(s.config.Username, s.config.AppPassword)
	return u.String()
}

func (s *BitbucketCloudSource) listAllRepos(ctx context.Context) ([]*bitbucketcloud.Repo, error) {
	type batch struct {
		repos []*bitbucketcloud.Repo
		err   error
	}

	ch := make(chan batch)

	var wg sync.WaitGroup

	wg.Add(1)
	go func(q string) {
		defer wg.Done()

		page := &bitbucketcloud.PageToken{Pagelen: 100}
		var err error
		var repos []*bitbucketcloud.Repo
		if repos, page, err = s.client.Repos(ctx, page, q); err != nil {
			ch <- batch{err: errors.Wrapf(err, "bibucketcloud.repositoryQuery: item=%q, page=%+v", q, page)}
			return
		}
		ch <- batch{repos: repos}

		for page.HasMore() {
			if page, err = s.client.ReqPage(ctx, page.Next, &repos); err != nil {
				ch <- batch{err: errors.Wrapf(err, "bibucketcloud.repositoryQuery: item=%q, page=%+v", q, page)}
				break
			}

			ch <- batch{repos: repos}
		}
	}("") // TODO(jchen): Fill in this parameter when support "repositoryQuery"

	go func() {
		wg.Wait()
		close(ch)
	}()

	seen := make(map[string]bool)
	errs := new(multierror.Error)
	var repos []*bitbucketcloud.Repo

	for r := range ch {
		if r.err != nil {
			errs = multierror.Append(errs, r.err)
		}

		for _, repo := range r.repos {
			// Discard non-Git repositories
			if repo.SCM != "git" {
				continue
			}

			if !seen[repo.UUID] { //&& !s.excludes(repo) { // TODO(jchen): Uncomment this logic when support "exclude"
				repos = append(repos, repo)
				seen[repo.UUID] = true
			}
		}
	}

	return repos, errs.ErrorOrNil()
}