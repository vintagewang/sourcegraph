package graphqlbackend

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	otlog "github.com/opentracing/opentracing-go/log"
	"github.com/xeonx/timeago"

	"github.com/pkg/errors"
	"github.com/sourcegraph/sourcegraph/cmd/frontend/db"
	"github.com/sourcegraph/sourcegraph/cmd/frontend/internal/pkg/search"
	"github.com/sourcegraph/sourcegraph/cmd/frontend/internal/pkg/search/query"
	"github.com/sourcegraph/sourcegraph/pkg/errcode"
	"github.com/sourcegraph/sourcegraph/pkg/trace"
	"github.com/sourcegraph/sourcegraph/pkg/vcs/git"
)

// commitSearchResultResolver is a resolver for the GraphQL type `CommitSearchResult`
type commitSearchResultResolver struct {
	commit         *gitCommitResolver
	refs           []*gitRefResolver
	sourceRefs     []*gitRefResolver
	messagePreview *highlightedString
	diffPreview    *highlightedString
	icon           string
	label          string
	url            string
	detail         string
	matches        []*searchResultMatchResolver
}

func (r *commitSearchResultResolver) Commit() *gitCommitResolver         { return r.commit }
func (r *commitSearchResultResolver) Refs() []*gitRefResolver            { return r.refs }
func (r *commitSearchResultResolver) SourceRefs() []*gitRefResolver      { return r.sourceRefs }
func (r *commitSearchResultResolver) MessagePreview() *highlightedString { return r.messagePreview }
func (r *commitSearchResultResolver) DiffPreview() *highlightedString    { return r.diffPreview }
func (r *commitSearchResultResolver) Icon() string {
	return r.icon
}

func (r *commitSearchResultResolver) Label() *markdownResolver {
	return &markdownResolver{text: r.label}
}

func (r *commitSearchResultResolver) URL() string {
	return r.url
}

func (r *commitSearchResultResolver) Detail() *markdownResolver {
	return &markdownResolver{text: r.detail}
}

func (r *commitSearchResultResolver) Matches() []*searchResultMatchResolver {
	return r.matches
}

var mockSearchCommitDiffsInRepo func(ctx context.Context, repoRevs search.RepositoryRevisions, info *search.PatternInfo, query *query.Query) (results []*commitSearchResultResolver, limitHit, timedOut bool, err error)

func searchCommitDiffsInRepo(ctx context.Context, repoRevs search.RepositoryRevisions, info *search.PatternInfo, query *query.Query) (results []*commitSearchResultResolver, limitHit, timedOut bool, err error) {
	if mockSearchCommitDiffsInRepo != nil {
		return mockSearchCommitDiffsInRepo(ctx, repoRevs, info, query)
	}

	textSearchOptions := git.TextSearchOptions{
		Pattern:         info.Pattern,
		IsRegExp:        info.IsRegExp,
		IsCaseSensitive: info.IsCaseSensitive,
	}
	return searchCommitsInRepo(ctx, commitSearchOp{
		repoRevs:          repoRevs,
		info:              info,
		query:             query,
		diff:              true,
		textSearchOptions: textSearchOptions,
	})
}

var mockSearchCommitLogInRepo func(ctx context.Context, repoRevs search.RepositoryRevisions, info *search.PatternInfo, query *query.Query) (results []*commitSearchResultResolver, limitHit, timedOut bool, err error)

func searchCommitLogInRepo(ctx context.Context, repoRevs search.RepositoryRevisions, info *search.PatternInfo, query *query.Query) (results []*commitSearchResultResolver, limitHit, timedOut bool, err error) {
	if mockSearchCommitLogInRepo != nil {
		return mockSearchCommitLogInRepo(ctx, repoRevs, info, query)
	}

	var terms []string
	if info.Pattern != "" {
		terms = append(terms, info.Pattern)
	}
	return searchCommitsInRepo(ctx, commitSearchOp{
		repoRevs:           repoRevs,
		info:               info,
		query:              query,
		diff:               false,
		textSearchOptions:  git.TextSearchOptions{},
		extraMessageValues: terms,
	})
}

type commitSearchOp struct {
	repoRevs           search.RepositoryRevisions
	info               *search.PatternInfo
	query              *query.Query
	diff               bool
	textSearchOptions  git.TextSearchOptions
	extraMessageValues []string
}

func searchCommitsInRepo(ctx context.Context, op commitSearchOp) (results []*commitSearchResultResolver, limitHit, timedOut bool, err error) {
	tr, ctx := trace.New(ctx, "searchCommitsInRepo", fmt.Sprintf("repoRevs: %v, pattern %+v", op.repoRevs, op.info))
	defer func() {
		tr.LazyPrintf("%d results, limitHit=%v, timedOut=%v", len(results), limitHit, timedOut)
		tr.SetError(err)
		tr.Finish()
	}()

	repo := op.repoRevs.Repo
	maxResults := int(op.info.FileMatchLimit)

	args := []string{
		"--no-prefix",
		"--max-count=" + strconv.Itoa(maxResults+1),
	}
	if op.diff {
		args = append(args,
			"--unified=0",
		)
	}
	if op.info.IsRegExp {
		args = append(args, "--extended-regexp")
	}
	if !op.query.IsCaseSensitive() {
		args = append(args, "--regexp-ignore-case")
	}

	for _, rev := range op.repoRevs.Revs {
		switch {
		case rev.RevSpec != "":
			if strings.HasPrefix(rev.RevSpec, "-") {
				// A revspec starting with "-" would be interpreted as a `git log` flag.
				// It would not be a security vulnerability because the flags are checked
				// against a whitelist, but it could cause unexpected errors by (e.g.)
				// changing the format of `git log` to a format that our parser doesn't
				// expect.
				return nil, false, false, fmt.Errorf("invalid revspec: %q", rev.RevSpec)
			}
			args = append(args, rev.RevSpec)

		case rev.RefGlob != "":
			args = append(args, "--glob="+rev.RefGlob)

		case rev.ExcludeRefGlob != "":
			args = append(args, "--exclude="+rev.ExcludeRefGlob)
		}
	}

	beforeValues, _ := op.query.StringValues(query.FieldBefore)
	for _, s := range beforeValues {
		args = append(args, "--until="+s)
	}
	afterValues, _ := op.query.StringValues(query.FieldAfter)
	for _, s := range afterValues {
		args = append(args, "--since="+s)
	}

	// Helper for adding git log flags --grep, --author, and --committer, which all behave similarly.
	var hasSeenGrepLikeFields, hasSeenInvertedGrepLikeFields bool
	addGrepLikeFlags := func(args *[]string, gitLogFlag string, field string, extraValues []string, expandUsernames bool) error {
		values, minusValues := op.query.RegexpPatterns(field)
		values = append(values, extraValues...)

		if expandUsernames {
			var err error
			values, err = expandUsernamesToEmails(ctx, values)
			if err != nil {
				return errors.WithMessage(err, fmt.Sprintf("expanding usernames in field %s", field))
			}
			minusValues, err = expandUsernamesToEmails(ctx, minusValues)
			if err != nil {
				return errors.WithMessage(err, fmt.Sprintf("expanding usernames in field -%s", field))
			}
		}

		hasSeenGrepLikeFields = hasSeenGrepLikeFields || len(values) > 0
		hasSeenInvertedGrepLikeFields = hasSeenInvertedGrepLikeFields || len(minusValues) > 0

		if hasSeenGrepLikeFields && hasSeenInvertedGrepLikeFields {
			// TODO(sqs): this is a limitation of `git log` flags, but we could overcome this
			// with post-filtering
			return errors.New("query not supported: combining message:/author:/committer: and -message/-author:/-committer: filters")
		}
		if len(values) > 0 || len(minusValues) > 0 {
			// To be consistent with how other filters work, always treat additional
			// filters as further constraining the result set, not widening it.
			*args = append(*args, "--all-match")

			if len(minusValues) > 0 {
				*args = append(*args, "--invert-grep")
			}

			// Only one of these for-loops will have any values to iterate over.
			for _, s := range values {
				*args = append(*args, gitLogFlag+"="+s)
			}
			for _, s := range minusValues {
				*args = append(*args, gitLogFlag+"="+s)
			}
		}
		return nil
	}
	if err := addGrepLikeFlags(&args, "--grep", query.FieldMessage, op.extraMessageValues, false); err != nil {
		return nil, false, false, err
	}
	if err := addGrepLikeFlags(&args, "--author", query.FieldAuthor, nil, true); err != nil {
		return nil, false, false, err
	}
	if err := addGrepLikeFlags(&args, "--committer", query.FieldCommitter, nil, true); err != nil {
		return nil, false, false, err
	}

	rawResults, complete, err := git.RawLogDiffSearch(ctx, op.repoRevs.GitserverRepo(), git.RawLogDiffSearchOptions{
		Query: op.textSearchOptions,
		Paths: git.PathOptions{
			IncludePatterns: op.info.IncludePatterns,
			ExcludePattern:  op.info.ExcludePattern,
			IsCaseSensitive: op.info.PathPatternsAreCaseSensitive,
			IsRegExp:        op.info.PathPatternsAreRegExps,
		},
		Diff:              op.diff,
		OnlyMatchingHunks: true,
		Args:              args,
	})
	if err != nil {
		return nil, false, false, err
	}

	// if the result is incomplete, git log timed out and the client should be notified of that
	timedOut = !complete
	if len(rawResults) > maxResults {
		limitHit = true
		rawResults = rawResults[:maxResults]
	}

	repoResolver := &repositoryResolver{repo: repo}
	results = make([]*commitSearchResultResolver, len(rawResults))
	for i, rawResult := range rawResults {
		commit := rawResult.Commit
		commitResolver := toGitCommitResolver(repoResolver, &commit)
		results[i] = &commitSearchResultResolver{commit: commitResolver}

		addRefs := func(dst *[]*gitRefResolver, src []string) {
			for _, ref := range src {
				*dst = append(*dst, &gitRefResolver{
					repo: repoResolver,
					name: ref,
				})
			}
		}
		addRefs(&results[i].refs, rawResult.Refs)
		addRefs(&results[i].sourceRefs, rawResult.SourceRefs)
		var matchBody string
		var matchHighlights []*highlightedRange
		// TODO(sqs): properly combine message: and term values for type:commit searches
		if !op.diff {
			var patString string
			if len(op.extraMessageValues) > 0 {
				patString = regexpPatternMatchingExprsInOrder(op.extraMessageValues)
				if !op.query.IsCaseSensitive() {
					patString = "(?i:" + patString + ")"
				}
				pat, err := regexp.Compile(patString)
				if err == nil {
					results[i].messagePreview = highlightMatches(pat, []byte(commit.Message))
					matchHighlights = results[i].messagePreview.highlights
				}
			} else {
				results[i].messagePreview = &highlightedString{value: string(commit.Message)}
			}
			matchBody = "```COMMIT_EDITMSG\n" + rawResult.Commit.Message + "\n```"
		}

		if rawResult.Diff != nil && op.diff {
			results[i].diffPreview = &highlightedString{
				value:      rawResult.Diff.Raw,
				highlights: fromVCSHighlights(rawResult.DiffHighlights),
			}
			matchBody, matchHighlights = cleanDiffPreview(fromVCSHighlights(rawResult.DiffHighlights), rawResult.Diff.Raw)
		}

		commitIcon := "data:image/svg+xml;base64,PD94bWwgdmVyc2lvbj0iMS4wIiBlbmNvZGluZz0iVVRGLTgiPz48IURPQ1RZUEUgc3ZnIFBVQkxJQyAiLS8vVzNDLy9EVEQgU1ZHIDEuMS8vRU4iICJodHRwOi8vd3d3LnczLm9yZy9HcmFwaGljcy9TVkcvMS4xL0RURC9zdmcxMS5kdGQiPjxzdmcgeG1sbnM9Imh0dHA6Ly93d3cudzMub3JnLzIwMDAvc3ZnIiB4bWxuczp4bGluaz0iaHR0cDovL3d3dy53My5vcmcvMTk5OS94bGluayIgdmVyc2lvbj0iMS4xIiB3aWR0aD0iMjQiIGhlaWdodD0iMjQiIHZpZXdCb3g9IjAgMCAyNCAyNCI+PHBhdGggZD0iTTE3LDEyQzE3LDE0LjQyIDE1LjI4LDE2LjQ0IDEzLDE2LjlWMjFIMTFWMTYuOUM4LjcyLDE2LjQ0IDcsMTQuNDIgNywxMkM3LDkuNTggOC43Miw3LjU2IDExLDcuMVYzSDEzVjcuMUMxNS4yOCw3LjU2IDE3LDkuNTggMTcsMTJNMTIsOUEzLDMgMCAwLDAgOSwxMkEzLDMgMCAwLDAgMTIsMTVBMywzIDAgMCwwIDE1LDEyQTMsMyAwIDAsMCAxMiw5WiIgLz48L3N2Zz4="
		results[i].label, err = createLabel(rawResult, commitResolver)
		if err != nil {
			return nil, false, false, err
		}
		commitHash := string(rawResult.Commit.ID)
		if len(rawResult.Commit.ID) > 7 {
			commitHash = string(rawResult.Commit.ID)[:7]
		}
		timeagoConfig := timeago.NoMax(timeago.English)

		url, err := commitResolver.URL()
		if err != nil {
			return nil, false, false, err
		}

		results[i].detail = fmt.Sprintf("[`%v` %v](%v)", commitHash, timeagoConfig.Format(rawResult.Commit.Author.Date), url)
		results[i].url = url
		results[i].icon = commitIcon
		match := &searchResultMatchResolver{body: matchBody, highlights: matchHighlights, url: url}
		matches := []*searchResultMatchResolver{match}
		results[i].matches = matches
	}

	return results, limitHit, timedOut, nil
}

func cleanDiffPreview(highlights []*highlightedRange, rawDiffResult string) (string, []*highlightedRange) {
	// A map of line number to number of lines that have been ignored before the particular line number.
	lineByCountIgnored := make(map[int]int32)
	// The line numbers of lines that were ignored.
	ignoredLineNumbers := make(map[int]bool)

	lines := strings.Split(rawDiffResult, "\n")
	var finalLines []string
	ignoreUntilAtAt := false
	var countIgnored int32
	for i, line := range lines {
		// ignore index, ---file, and +++file lines
		if ignoreUntilAtAt && !strings.HasPrefix(line, "@@ ") {
			ignoredLineNumbers[i] = true
			countIgnored++
			continue
		} else {
			ignoreUntilAtAt = false
		}
		if strings.HasPrefix(line, "diff ") {
			ignoreUntilAtAt = true
			lineByCountIgnored[i] = countIgnored
			l := strings.Replace(line, "diff --git ", "", 1)
			finalLines = append(finalLines, l)
		} else {
			lineByCountIgnored[i] = countIgnored
			finalLines = append(finalLines, line)
		}
	}

	for n := range highlights {
		// For each highlight, adjust the line number by the number of lines that were
		// ignored in the diff before.
		linesIgnored := lineByCountIgnored[int(highlights[n].line)]
		if ignoredLineNumbers[int(highlights[n].line)-1] {
			// Effectively remove highlights that were on ignored lines by setting
			// line to -1.
			highlights[n].line = -1
		}
		if linesIgnored > 0 {
			highlights[n].line = highlights[n].line - linesIgnored
		}
	}

	body := fmt.Sprintf("```diff\n%v```", strings.Join(finalLines, "\n"))
	return body, highlights
}

func createLabel(rawResult *git.LogCommitSearchResult, commitResolver *gitCommitResolver) (string, error) {
	message := commitSubject(rawResult.Commit.Message)
	author := rawResult.Commit.Author.Name
	repoName := displayRepoName(commitResolver.Repository().Name())
	repoURL := commitResolver.Repository().URL()
	url, err := commitResolver.URL()
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("[%s](%s) › [%s](%s): [%s](%s)", repoName, repoURL, author, url, message, url), nil
}

func commitSubject(message string) string {
	idx := strings.Index(message, "\n")
	if idx != -1 {
		return message[:idx]
	}
	return message
}

func displayRepoName(repoPath string) string {
	parts := strings.Split(repoPath, "/")
	if len(parts) >= 3 && strings.Contains(parts[0], ".") {
		parts = parts[1:] // remove hostname from repo path (reduce visual noise)
	}
	return strings.Join(parts, "/")
}

func highlightMatches(pattern *regexp.Regexp, data []byte) *highlightedString {
	const maxMatchesPerLine = 25 // arbitrary

	var highlights []*highlightedRange
	for i, line := range bytes.Split(data, []byte("\n")) {
		for _, match := range pattern.FindAllIndex(bytes.ToLower(line), maxMatchesPerLine) {
			highlights = append(highlights, &highlightedRange{
				line:      int32(i + 1),
				character: int32(match[0]),
				length:    int32(match[1] - match[0]),
			})
		}
	}
	return &highlightedString{
		value:      string(data),
		highlights: highlights,
	}
}

var mockSearchCommitDiffsInRepos func(args *search.Args) ([]*searchResultResolver, *searchResultsCommon, error)

// searchCommitDiffsInRepos searches a set of repos for matching commit diffs.
func searchCommitDiffsInRepos(ctx context.Context, args *search.Args) ([]*searchResultResolver, *searchResultsCommon, error) {
	if mockSearchCommitDiffsInRepos != nil {
		return mockSearchCommitDiffsInRepos(args)
	}

	var err error
	tr, ctx := trace.New(ctx, "searchCommitDiffsInRepos", fmt.Sprintf("query: %+v, numRepoRevs: %d", args.Pattern, len(args.Repos)))
	defer func() {
		tr.SetError(err)
		tr.Finish()
	}()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg          sync.WaitGroup
		mu          sync.Mutex
		unflattened [][]*commitSearchResultResolver
		common      = &searchResultsCommon{}
	)
	for _, repoRev := range args.Repos {
		wg.Add(1)
		go func(repoRev search.RepositoryRevisions) {
			defer wg.Done()
			results, repoLimitHit, repoTimedOut, searchErr := searchCommitDiffsInRepo(ctx, repoRev, args.Pattern, args.Query)
			if ctx.Err() == context.Canceled {
				// Our request has been canceled (either because another one of args.repos had a
				// fatal error, or otherwise), so we can just ignore these results.
				return
			}
			repoTimedOut = repoTimedOut || ctx.Err() == context.DeadlineExceeded
			if searchErr != nil {
				tr.LogFields(otlog.String("repo", string(repoRev.Repo.Name)), otlog.String("searchErr", searchErr.Error()), otlog.Bool("timeout", errcode.IsTimeout(searchErr)), otlog.Bool("temporary", errcode.IsTemporary(searchErr)), otlog.Bool("timeout", errcode.IsTimeout(searchErr)), otlog.Bool("temporary", errcode.IsTemporary(searchErr)))
			}
			mu.Lock()
			defer mu.Unlock()
			if fatalErr := handleRepoSearchResult(common, repoRev, repoLimitHit, repoTimedOut, searchErr); fatalErr != nil {
				err = errors.Wrapf(searchErr, "failed to search commit diffs %s", repoRev.String())
				cancel()
			}
			if len(results) > 0 {
				unflattened = append(unflattened, results)
			}
		}(*repoRev)
	}
	wg.Wait()
	if err != nil {
		return nil, nil, err
	}

	var flattened []*commitSearchResultResolver
	for _, results := range unflattened {
		flattened = append(flattened, results...)
	}
	return commitSearchResultsToSearchResults(flattened), common, nil
}

var mockSearchCommitLogInRepos func(args *search.Args) ([]*searchResultResolver, *searchResultsCommon, error)

// searchCommitLogInRepos searches a set of repos for matching commits.
func searchCommitLogInRepos(ctx context.Context, args *search.Args) ([]*searchResultResolver, *searchResultsCommon, error) {
	if mockSearchCommitLogInRepos != nil {
		return mockSearchCommitLogInRepos(args)
	}

	var err error
	tr, ctx := trace.New(ctx, "searchCommitLogInRepos", fmt.Sprintf("query: %+v, numRepoRevs: %d", args.Pattern, len(args.Repos)))
	defer func() {
		tr.SetError(err)
		tr.Finish()
	}()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg          sync.WaitGroup
		mu          sync.Mutex
		unflattened [][]*commitSearchResultResolver
		common      = &searchResultsCommon{}
	)
	for _, repoRev := range args.Repos {
		wg.Add(1)
		go func(repoRev search.RepositoryRevisions) {
			defer wg.Done()
			results, repoLimitHit, repoTimedOut, searchErr := searchCommitLogInRepo(ctx, repoRev, args.Pattern, args.Query)
			if ctx.Err() == context.Canceled {
				// Our request has been canceled (either because another one of args.repos had a
				// fatal error, or otherwise), so we can just ignore these results.
				return
			}
			repoTimedOut = repoTimedOut || ctx.Err() == context.DeadlineExceeded
			if searchErr != nil {
				tr.LogFields(otlog.String("repo", string(repoRev.Repo.Name)), otlog.String("searchErr", searchErr.Error()), otlog.Bool("timeout", errcode.IsTimeout(searchErr)), otlog.Bool("temporary", errcode.IsTemporary(searchErr)))
			}
			mu.Lock()
			defer mu.Unlock()
			if fatalErr := handleRepoSearchResult(common, repoRev, repoLimitHit, repoTimedOut, searchErr); fatalErr != nil {
				err = errors.Wrapf(searchErr, "failed to search commit log %s", repoRev.String())
				cancel()
			}
			if len(results) > 0 {
				unflattened = append(unflattened, results)
			}
		}(*repoRev)
	}
	wg.Wait()
	if err != nil {
		return nil, nil, err
	}

	var flattened []*commitSearchResultResolver
	for _, results := range unflattened {
		flattened = append(flattened, results...)
	}
	return commitSearchResultsToSearchResults(flattened), common, nil
}

func commitSearchResultsToSearchResults(results []*commitSearchResultResolver) []*searchResultResolver {
	// Show most recent commits first.
	sort.Slice(results, func(i, j int) bool {
		return results[i].commit.author.Date() > results[j].commit.author.Date()
	})

	results2 := make([]*searchResultResolver, len(results))
	for i, result := range results {
		results2[i] = &searchResultResolver{diff: result}
	}
	return results2
}

// expandUsernamesToEmails expands references to usernames to mention all possible (known and
// verified) email addresses for the user.
//
// For example, given a list ["foo", "@alice"] where the user "alice" has 2 email addresses
// "alice@example.com" and "alice@example.org", it would return ["foo", "alice@example\\.com",
// "alice@example\\.org"].
func expandUsernamesToEmails(ctx context.Context, values []string) (expandedValues []string, err error) {
	expandOne := func(ctx context.Context, value string) ([]string, error) {
		if isPossibleUsernameReference := strings.HasPrefix(value, "@"); !isPossibleUsernameReference {
			return nil, nil
		}

		user, err := db.Users.GetByUsername(ctx, strings.TrimPrefix(value, "@"))
		if errcode.IsNotFound(err) {
			return nil, nil
		} else if err != nil {
			return nil, err
		}
		emails, err := db.UserEmails.ListByUser(ctx, user.ID)
		if err != nil {
			return nil, err
		}
		values := make([]string, 0, len(emails))
		for _, email := range emails {
			if email.VerifiedAt != nil {
				values = append(values, regexp.QuoteMeta(email.Email))
			}
		}
		return values, nil
	}

	expandedValues = make([]string, 0, len(values))
	for _, v := range values {
		x, err := expandOne(ctx, v)
		if err != nil {
			return nil, err
		}
		if x == nil {
			expandedValues = append(expandedValues, v) // not a username or couldn't expand
		} else {
			expandedValues = append(expandedValues, x...)
		}
	}
	return expandedValues, nil
}
