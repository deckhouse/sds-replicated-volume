package rvr

type Request interface {
	_isRequest()
}

// single resource was created or spec has changed
type ResourceReconcileRequest struct {
	Name string
}

func (r ResourceReconcileRequest) _isRequest() {}

// single resource was deleted and needs cleanup
type ResourceDeleteRequest struct {
	Name                 string
	ReplicatedVolumeName string
}

func (r ResourceDeleteRequest) _isRequest() {}

var _ Request = ResourceReconcileRequest{}
var _ Request = ResourceDeleteRequest{}
