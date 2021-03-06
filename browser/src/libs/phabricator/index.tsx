import { FileSpec, RawRepoSpec, ResolvedRevSpec, RevSpec } from '../../../../shared/src/util/url'

export enum PhabricatorMode {
    Diffusion = 1,
    Differential,
    Revision,
    Change,
}

export interface DiffusionState extends RawRepoSpec, RevSpec, ResolvedRevSpec, FileSpec {
    mode: PhabricatorMode
}

export interface DifferentialState {
    mode: PhabricatorMode
    differentialID: number
    leftDiffID?: number
    diffID?: number
    baseRev: string
    baseRawRepoName: string
    headRev: string
    headRawRepoName: string
}

export interface RevisionState extends RawRepoSpec {
    mode: PhabricatorMode
    baseCommitID: string
    headCommitID: string
}

/**
 * Refers to a URL like http://phabricator.aws.sgdev.org/source/nzap/change/master/checked_message_bench_test.go,
 * which a user gets to by clicking "Show Last Change" on a differential page.
 */
export interface ChangeState extends RawRepoSpec, FileSpec, ResolvedRevSpec {
    mode: PhabricatorMode
}

export function convertSpacesToTabs(realLineContent: string, domContent: string): boolean {
    return !!realLineContent && !!domContent && realLineContent.startsWith('\t') && !domContent.startsWith('\t')
}

export function spacesToTabsAdjustment(text: string): number {
    let suffix = text
    let adjustment = 0

    while (suffix.length >= 2 && suffix.startsWith('  ')) {
        ++adjustment
        suffix = suffix.substr(2)
    }
    return adjustment
}
