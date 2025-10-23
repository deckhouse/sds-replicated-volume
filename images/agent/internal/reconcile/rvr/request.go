package rvr

type Request interface {
	_isRequest()
}

// single resource was created or spec has changed
type ResourceReconcileRequest struct {
	Name string
}

var _ Request = ResourceReconcileRequest{}

func (r ResourceReconcileRequest) _isRequest() {}

// single resource was deleted and needs cleanup
type ResourceDeleteRequest struct {
	Name                 string
	ReplicatedVolumeName string
}

var _ Request = ResourceDeleteRequest{}

func (r ResourceDeleteRequest) _isRequest() {}

// special request: force primary when annotation is added
type ResourcePrimaryForceRequest struct {
	Name string
}

func (r ResourcePrimaryForceRequest) _isRequest() {}

var _ Request = ResourcePrimaryForceRequest{}

// special request: resize resource when annotation is added
type ResourceResizeRequest struct {
	Name string
}

func (r ResourceResizeRequest) _isRequest() {}

var _ Request = ResourceResizeRequest{}
