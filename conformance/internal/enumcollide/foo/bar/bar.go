package bar

// X is an enum whose helper name collides with foo_bar.Mode_X after sanitizing.
type X string

const XB X = "b"
