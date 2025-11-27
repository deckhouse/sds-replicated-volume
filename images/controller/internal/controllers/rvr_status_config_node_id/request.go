package rvrstatusconfignodeid

type Request interface {
	_isRequest()
}

type AssignNodeIDRequest struct {
	Name string
}

func (r AssignNodeIDRequest) _isRequest() {}

var _ Request = AssignNodeIDRequest{}
