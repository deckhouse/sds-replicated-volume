package cluster

import (
	"fmt"
	"reflect"
	"strings"
)

type Diff interface {
	OldValue() any
	NewValue() any
}

type diff struct {
	oldValue any
	newValue any
}

var _ Diff = diff{}

func (f diff) NewValue() any {
	return f.newValue
}

func (f diff) OldValue() any {
	return f.oldValue
}

type ChangeSet map[string]Diff

func (cs ChangeSet) String() string {
	var sb strings.Builder

	var addSpace bool
	for name, diff := range cs {
		if addSpace {
			sb.WriteString(" ")
		} else {
			addSpace = true
		}
		sb.WriteString(name)
		sb.WriteString(": ")
		sb.WriteString(fmt.Sprint(diff.OldValue()))
		sb.WriteString(" -> ")
		sb.WriteString(fmt.Sprint(diff.NewValue()))
		sb.WriteString(";")
	}

	return sb.String()
}

func Change[T comparable](changeSet ChangeSet, name string, oldValuePtr *T, newValue T) ChangeSet {
	if *oldValuePtr == newValue {
		return changeSet
	}
	return addChange(changeSet, name, oldValuePtr, newValue)
}

func ChangeEqualFn[T any](changeSet ChangeSet, name string, oldValuePtr *T, newValue T, eq func(any, any) bool) ChangeSet {
	if eq(*oldValuePtr, newValue) {
		return changeSet
	}

	return addChange(changeSet, name, oldValuePtr, newValue)
}

func ChangeDeepEqual[T any](changeSet ChangeSet, name string, oldValuePtr *T, newValue T) ChangeSet {
	if reflect.DeepEqual(*oldValuePtr, newValue) {
		return changeSet
	}
	return addChange(changeSet, name, oldValuePtr, newValue)
}

func addChange[T any](changeSet ChangeSet, name string, oldValuePtr *T, newValue T) ChangeSet {
	d := diff{
		oldValue: *oldValuePtr,
		newValue: newValue,
	}

	*oldValuePtr = newValue

	if changeSet == nil {
		changeSet = make(ChangeSet, 1)
	}
	changeSet[name] = d
	return changeSet
}
