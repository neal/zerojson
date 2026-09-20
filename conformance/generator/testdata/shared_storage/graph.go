package p

type Node struct {
	N int
	P *int
	S []int
}
type Graph struct {
	A     map[string]Node
	B     map[string]Node
	Items []Node
	P     *int
	Child Node
	C     int
}
