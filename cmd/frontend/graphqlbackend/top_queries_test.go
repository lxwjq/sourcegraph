package graphqlbackend

import (
	"context"
	"github.com/sourcegraph/sourcegraph/cmd/frontend/db"
	"reflect"
	"testing"
)

func Test_schemaResolver_TopQueries(t *testing.T) {
	type args struct {
		Limit int32
	}
	tests := []struct {
		name    string
		args    args
		queries []string
		want    []queryCountResolver
	}{
		{
			name:    "empty case",
			args:    args{Limit: 10},
			queries: nil,
			want:    nil,
		},
		{
			name:    "single query",
			args:    args{Limit: 10},
			queries: []string{""},
			want: []queryCountResolver{
				{query: "", count: 1},
			},
		},
		{
			name:    "two of the same query",
			args:    args{Limit: 10},
			queries: []string{"", ""},
			want: []queryCountResolver{
				{query: "", count: 2},
			},
		},
		{
			name:    "two different queries",
			args:    args{Limit: 10},
			queries: []string{"a", "b"},
			want: []queryCountResolver{
				{query: "a", count: 1},
				{query: "b", count: 1},
			},
		},
		{
			name:    "can limit queries",
			args:    args{Limit: 1},
			queries: []string{"a", "b"},
			want: []queryCountResolver{
				{query: "a", count: 1},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rsm := &RecentSearchesMock{
				queries: tt.queries,
			}
			s := &schemaResolver{
				recentSearches: rsm,
			}
			got, err := s.TopQueries(context.Background(), (*struct{ Limit int32 })(&tt.args))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("schemaResolver.TopQueries() = %v, want %v", got, tt.want)
			}
		})
	}
}

type RecentSearchesMock struct {
	queries []string

	// Default unimplemented interface methods to nil panic.
	db.RecentSearchesTracker
}

func (rsm *RecentSearchesMock) Get(ctx context.Context) ([]string, error) {
	return rsm.queries, nil
}
