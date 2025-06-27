package rvr

type Request interface {
	_isRequest()
}

// single resource was created or spec has changed
type ResourceReconcileRequest struct {
	Name string
}

func (r ResourceReconcileRequest) _isRequest() {}

var _ Request = ResourceReconcileRequest{}
