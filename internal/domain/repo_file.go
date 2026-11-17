package domain

// RepoFile is a file of a repository at a ref, with the blob version a write
// must name so that it never overwrites a change it did not see (D117).
type RepoFile struct {
	Content []byte
	BlobSHA string
}

// Commit is one commit of a file's history, newest first in listings.
type Commit struct {
	SHA       string
	Message   string
	ParentSHA string // the first parent; empty for a root commit
}
