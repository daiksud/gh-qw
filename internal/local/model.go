package local

// Repository is one registered main checkout and its filesystem boundary.
type Repository struct {
	Identity string
	Host     string
	Owner    string
	Repo     string
	Path     string
	Root     string
}

// Worktree is one worktree registered with a Repository.
type Worktree struct {
	Repository Repository
	Identity   string
	Slot       string
	Path       string
	HEAD       string
	Branch     string

	Main           bool
	Detached       bool
	Bare           bool
	Locked         bool
	LockedReason   string
	Prunable       bool
	PrunableReason string
}

// Current identifies the registered repository and registered worktree that
// contain the current directory.
type Current struct {
	Repository Repository
	Worktree   Worktree
}
