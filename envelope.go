package melody

type envelope struct {
	T      int
	Msg    []byte
	filter filterFunc
	To     interface{}
	From   uintptr
}
