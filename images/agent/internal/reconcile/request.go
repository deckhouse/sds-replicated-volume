package reconcile

import (
	"github.com/google/uuid"
)

type TypedRequest[T any] interface {
	RequestId() string
	IsCreate() bool
	IsUpdate() bool
	IsDelete() bool
	Object() T
	OldObject() T
}

type typedRequest[T any] struct {
	reqId  string
	objOld *T
	objNew *T
}

func (req *typedRequest[T]) IsCreate() bool {
	return req.objOld == nil
}

func (req *typedRequest[T]) IsDelete() bool {
	return req.objNew == nil
}

func (req *typedRequest[T]) IsUpdate() bool {
	return req.objNew != nil && req.objOld != nil
}

func (req *typedRequest[T]) Object() T {
	if req.objNew != nil {
		return *req.objNew
	}
	return *req.objOld
}

func (req *typedRequest[T]) OldObject() T {
	if req.objOld != nil {
		return *req.objOld
	}
	return *req.objNew
}

func (req *typedRequest[T]) RequestId() string {
	panic("unimplemented")
}

func NewTypedRequestCreate[T any](obj T) TypedRequest[T] {
	return &typedRequest[T]{
		reqId:  newRandomRequestId("CREATE#"),
		objNew: &obj,
	}
}

func NewTypedRequestUpdate[T any](objOld T, objNew T) TypedRequest[T] {
	return &typedRequest[T]{
		reqId:  newRandomRequestId("UPDATE#"),
		objOld: &objOld,
		objNew: &objNew,
	}

}

func NewTypedRequestDelete[T any](obj T) TypedRequest[T] {
	return &typedRequest[T]{
		reqId:  newRandomRequestId("DELETE#"),
		objOld: &obj,
	}
}

func newRandomRequestId(requestType string) string {
	return requestType + uuid.NewString()
}
