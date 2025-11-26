package rvrdiskfulcount

type Request interface {
	_isRequest()
}

//

type AddFirstRequest struct {
	Name string
}

type AddSubsequentRequest struct {
	Name string
}

// ...

func (r AddFirstRequest) _isRequest()      {}
func (r AddSubsequentRequest) _isRequest() {}

// ...

var _ Request = AddFirstRequest{}
var _ Request = AddSubsequentRequest{}

// ...
