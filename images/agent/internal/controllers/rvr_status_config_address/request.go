package rvrstatusconfigaddress

type Request interface {
	_isRequest()
}

//

type MainRequest struct {
	Name string
}

type AlternativeRequest struct {
	Name string
}

// ...

func (r MainRequest) _isRequest()        {}
func (r AlternativeRequest) _isRequest() {}

// ...

var _ Request = MainRequest{}
var _ Request = AlternativeRequest{}

// ...
