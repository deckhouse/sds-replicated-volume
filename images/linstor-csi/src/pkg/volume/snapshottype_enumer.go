// Code generated by "enumer -type=SnapshotType -trimprefix=SnapshotType"; DO NOT EDIT.

package volume

import (
	"fmt"
)

const _SnapshotTypeName = "InClusterS3Linstor"

var _SnapshotTypeIndex = [...]uint8{0, 9, 11, 18}

func (i SnapshotType) String() string {
	if i < 0 || i >= SnapshotType(len(_SnapshotTypeIndex)-1) {
		return fmt.Sprintf("SnapshotType(%d)", i)
	}
	return _SnapshotTypeName[_SnapshotTypeIndex[i]:_SnapshotTypeIndex[i+1]]
}

var _SnapshotTypeValues = []SnapshotType{0, 1, 2}

var _SnapshotTypeNameToValueMap = map[string]SnapshotType{
	_SnapshotTypeName[0:9]:   0,
	_SnapshotTypeName[9:11]:  1,
	_SnapshotTypeName[11:18]: 2,
}

// SnapshotTypeString retrieves an enum value from the enum constants string name.
// Throws an error if the param is not part of the enum.
func SnapshotTypeString(s string) (SnapshotType, error) {
	if val, ok := _SnapshotTypeNameToValueMap[s]; ok {
		return val, nil
	}
	return 0, fmt.Errorf("%s does not belong to SnapshotType values", s)
}

// SnapshotTypeValues returns all values of the enum
func SnapshotTypeValues() []SnapshotType {
	return _SnapshotTypeValues
}

// IsASnapshotType returns "true" if the value is listed in the enum definition. "false" otherwise
func (i SnapshotType) IsASnapshotType() bool {
	for _, v := range _SnapshotTypeValues {
		if i == v {
			return true
		}
	}
	return false
}