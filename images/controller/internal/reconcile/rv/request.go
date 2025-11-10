package rv

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
	Name string
}

func (r ResourceDeleteRequest) _isRequest() {}

// children (RVR/LLV) status changed; refresh RV Ready condition
type ResourceStatusReconcileRequest struct {
	Name string
}

func (r ResourceStatusReconcileRequest) _isRequest() {}

var _ Request = ResourceReconcileRequest{}
var _ Request = ResourceDeleteRequest{}
var _ Request = ResourceStatusReconcileRequest{}
