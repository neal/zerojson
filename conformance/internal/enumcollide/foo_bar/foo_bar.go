package foo_bar

// Mode_X is an enum whose helper name collides with foo/bar.X after sanitizing.
type Mode_X string

const ModeA Mode_X = "a"
