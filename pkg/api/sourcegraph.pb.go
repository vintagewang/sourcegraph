package sourcegraph

import (
	"fmt"
	"time"

	"github.com/sourcegraph/go-langserver/pkg/lspext"
)

// Repo represents a source code repository.
type Repo struct {
	// ID is the unique numeric ID for this repository.
	ID int32 `json:"ID,omitempty"`
	// URI is a normalized identifier for this repository based on its primary clone
	// URL. E.g., "github.com/user/repo".
	URI string `json:"URI,omitempty"`
	// Description is a brief description of the repository.
	Description string `json:"Description,omitempty"`
	// Language is the primary programming language used in this repository.
	Language string `json:"Language,omitempty"`
	// Enabled is whether the repository is enabled. Disabled repositories are
	// not accessible by users (except site admins).
	Enabled bool `json:"Enabled,omitempty"`
	// Fork is whether this repository is a fork.
	Fork bool `json:"Fork,omitempty"`
	// StarsCount is the number of users who have starred this repository.
	// Not persisted in DB!
	StarsCount *uint `json:"Stars,omitempty"`
	// ForksCount is the number of forks of this repository that exist.
	// Not persisted in DB!
	ForksCount *uint `json:"Forks,omitempty"`
	// Private is whether this repository is private. Note: this field
	// is currently only used when the repository is hosted on GitHub.
	// All locally hosted repositories should be public. If Private is
	// true for a locally hosted repository, the repository might never
	// be returned.
	Private bool `json:"Private,omitempty"`
	// CreatedAt is when this repository was created. If it represents an externally
	// hosted (e.g., GitHub) repository, the creation date is when it was created at
	// that origin.
	CreatedAt *time.Time `json:"CreatedAt,omitempty"`
	// UpdatedAt is when this repository's metadata was last updated (on its origin if
	// it's an externally hosted repository).
	UpdatedAt *time.Time `json:"UpdatedAt,omitempty"`
	// PushedAt is when this repository's was last (VCS-)pushed to.
	PushedAt *time.Time `json:"PushedAt,omitempty"`
	// IndexedRevision is the revision that the global index is currently based on. It is only used
	// by the indexer to determine if reindexing is necessary. Setting it to nil/null will cause
	// the indexer to reindex the next time it gets triggered for this repository.
	IndexedRevision *string `json:"IndexedRevision,omitempty"`
	// FreezeIndexedRevision, when true, tells the indexer not to
	// update the indexed revision if it is already set. This is a
	// kludge that lets us freeze the indexed repository revision for
	// specific deployments
	FreezeIndexedRevision bool `json:"FreezeIndexedRevision,omitempty"`
}

// RepoRevSpec specifies a repository at a specific commit.
type RepoRevSpec struct {
	Repo int32 `json:"Repo,omitempty"`
	// CommitID is the 40-character SHA-1 of the Git commit ID.
	//
	// Revision specifiers are not allowed here. To resolve a revision
	// specifier (such as a branch name or "master~7"), call
	// Repos.GetCommit.
	CommitID string `json:"CommitID,omitempty"`
}

// RepoSpec specifies a repository.
type RepoSpec struct {
	ID int32 `json:"ID,omitempty"`
}

// DependencyReferencesOptions specifies options for querying dependency references.
type DependencyReferencesOptions struct {
	Language        string // e.g. "go"
	RepoID          int32  // repository whose file:line:character describe the symbol of interest
	CommitID        string
	File            string
	Line, Character int

	// Limit specifies the number of dependency references to return.
	Limit int // e.g. 20
}

type DependencyReferences struct {
	References []*DependencyReference
	Location   lspext.SymbolLocationInformation
}

// DependencyReference effectively says that RepoID has made a reference to a
// dependency.
type DependencyReference struct {
	DepData map[string]interface{} // includes additional information about the dependency, e.g. whether or not it is vendored for Go
	RepoID  int32                  // the repository who made the reference to the dependency.
	Hints   map[string]interface{} // hints which should be passed to workspace/xreferences in order to more quickly find the definition.
}

func (d *DependencyReference) String() string {
	return fmt.Sprintf("DependencyReference{DepData: %v, RepoID: %v, Hints: %v}", d.DepData, d.RepoID, d.Hints)
}

const (
	// UserProviderHTTPHeader is the http-header auth provider.
	UserProviderHTTPHeader = "http-header"
)

// User represents a registered user.
type User struct {
	ID               int32     `json:"ID,omitempty"`
	ExternalID       *string   `json:"externalID,omitempty"`
	Username         string    `json:"username,omitempty"`
	ExternalProvider string    `json:"externalProvider,omitempty"`
	DisplayName      string    `json:"displayName,omitempty"`
	AvatarURL        *string   `json:"avatarURL,omitempty"`
	CreatedAt        time.Time `json:"createdAt,omitempty"`
	UpdatedAt        time.Time `json:"updatedAt,omitempty"`
	SiteAdmin        bool      `json:"siteAdmin,omitempty"`
}

// OrgRepo represents a repo that exists on a native client's filesystem, but
// does not necessarily have its contents cloned to a remote Sourcegraph server.
type OrgRepo struct {
	ID                int32
	CanonicalRemoteID string
	CloneURL          string
	OrgID             int32
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type ThreadLines struct {
	// HTMLBefore is unsanitized HTML lines before the user selection.
	HTMLBefore string `json:"HTMLBefore,omitempty"`

	// HTML is unsanitized HTML lines of the user selection.
	HTML string `json:"HTML,omitempty"`

	// HTMLAfter is unsanitized HTML lines after the user selection.
	HTMLAfter                string `json:"HTMLAfter,omitempty"`
	TextBefore               string `json:"TextBefore,omitempty"`
	Text                     string `json:"Text,omitempty"`
	TextAfter                string `json:"TextAfter,omitempty"`
	TextSelectionRangeStart  int32  `json:"TextSelectionRangeStart,omitempty"`
	TextSelectionRangeLength int32  `json:"TextSelectionRangeLength,omitempty"`
}

type Thread struct {
	ID                int32        `json:"ID,omitempty"`
	OrgRepoID         int32        `json:"OrgRepoID,omitempty"`
	RepoRevisionPath  string       `json:"RepoRevisionPath,omitempty"`
	LinesRevisionPath string       `json:"LinesRevisionPath,omitempty"`
	RepoRevision      string       `json:"RepoRevision,omitempty"`
	LinesRevision     string       `json:"LinesRevision,omitempty"`
	Branch            *string      `json:"Branch,omitempty"`
	StartLine         int32        `json:"StartLine,omitempty"`
	EndLine           int32        `json:"EndLine,omitempty"`
	StartCharacter    int32        `json:"StartCharacter,omitempty"`
	EndCharacter      int32        `json:"EndCharacter,omitempty"`
	RangeLength       int32        `json:"RangeLength,omitempty"`
	CreatedAt         time.Time    `json:"CreatedAt,omitempty"`
	UpdatedAt         time.Time    `json:"UpdatedAt,omitempty"`
	ArchivedAt        *time.Time   `json:"ArchivedAt,omitempty"`
	AuthorUserID      int32        `json:"AuthorUserID,omitempty"`
	Lines             *ThreadLines `json:"ThreadLines,omitempty"`
}

type Comment struct {
	ID           int32     `json:"ID,omitempty"`
	ThreadID     int32     `json:"ThreadID,omitempty"`
	Contents     string    `json:"Contents,omitempty"`
	CreatedAt    time.Time `json:"CreatedAt,omitempty"`
	UpdatedAt    time.Time `json:"UpdatedAt,omitempty"`
	AuthorUserID int32     `json:"AuthorUserID,omitempty"`
}

// SharedItem represents a shared thread or comment. Note that a code snippet
// is also just a thread.
type SharedItem struct {
	ULID         string `json:"ULID"`
	Public       bool   `json:"public"`
	AuthorUserID int32  `json:"AuthorUserID"`
	ThreadID     *int32 `json:"ThreadID,omitempty"`
	CommentID    *int32 `json:"CommentID,omitempty"` // optional
}

type Org struct {
	ID              int32     `json:"ID"`
	Name            string    `json:"Name,omitempty"`
	DisplayName     *string   `json:"DisplayName,omitempty"`
	SlackWebhookURL *string   `json:"SlackWebhookURL,omitempty"`
	CreatedAt       time.Time `json:"CreatedAt,omitempty"`
	UpdatedAt       time.Time `json:"UpdatedAt,omitempty"`
}

type OrgMember struct {
	ID        int32     `json:"ID"`
	OrgID     int32     `json:"OrgID"`
	UserID    int32     `json:"UserID"`
	CreatedAt time.Time `json:"CreatedAt,omitempty"`
	UpdatedAt time.Time `json:"UpdatedAt,omitempty"`
}

type UserTag struct {
	ID     int32  `json:"ID"`
	UserID int32  `json:"UserID"`
	Name   string `json:"Name,omitempty"`
}

type OrgTag struct {
	ID    int32  `json:"ID"`
	OrgID int32  `json:"OrgID"`
	Name  string `json:"Name,omitempty"`
}

type PhabricatorRepo struct {
	ID       int32  `json:"ID"`
	URI      string `json:"URI"`
	URL      string `json:"URL"`
	Callsign string `json:"Callsign"`
}

type UserActivity struct {
	UserID        int32
	PageViews     int32
	SearchQueries int32
}
