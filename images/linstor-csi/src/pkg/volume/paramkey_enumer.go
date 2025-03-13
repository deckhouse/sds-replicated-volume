// Code generated by "enumer -type=paramKey"; DO NOT EDIT.

package volume

import (
	"fmt"
)

const _paramKeyName = "allowremotevolumeaccessautoplaceclientlistdisklessonremainingdisklessstoragepooldonotplacewithregexencryptionfsoptslayerlistmountoptsnodelistplacementcountplacementpolicyreplicasondifferentreplicasonsamesizekibstoragepoolpostmountxfsoptsresourcegroupusepvcnameoverprovisionstorageclassname"

var _paramKeyIndex = [...]uint16{0, 23, 32, 42, 61, 80, 99, 109, 115, 124, 133, 141, 155, 170, 189, 203, 210, 221, 237, 250, 260, 273, 288}

func (i paramKey) String() string {
	if i < 0 || i >= paramKey(len(_paramKeyIndex)-1) {
		return fmt.Sprintf("paramKey(%d)", i)
	}
	return _paramKeyName[_paramKeyIndex[i]:_paramKeyIndex[i+1]]
}

var _paramKeyValues = []paramKey{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21}

var _paramKeyNameToValueMap = map[string]paramKey{
	_paramKeyName[0:23]:    0,
	_paramKeyName[23:32]:   1,
	_paramKeyName[32:42]:   2,
	_paramKeyName[42:61]:   3,
	_paramKeyName[61:80]:   4,
	_paramKeyName[80:99]:   5,
	_paramKeyName[99:109]:  6,
	_paramKeyName[109:115]: 7,
	_paramKeyName[115:124]: 8,
	_paramKeyName[124:133]: 9,
	_paramKeyName[133:141]: 10,
	_paramKeyName[141:155]: 11,
	_paramKeyName[155:170]: 12,
	_paramKeyName[170:189]: 13,
	_paramKeyName[189:203]: 14,
	_paramKeyName[203:210]: 15,
	_paramKeyName[210:221]: 16,
	_paramKeyName[221:237]: 17,
	_paramKeyName[237:250]: 18,
	_paramKeyName[250:260]: 19,
	_paramKeyName[260:273]: 20,
	_paramKeyName[273:288]: 21,
}

// paramKeyString retrieves an enum value from the enum constants string name.
// Throws an error if the param is not part of the enum.
func paramKeyString(s string) (paramKey, error) {
	if val, ok := _paramKeyNameToValueMap[s]; ok {
		return val, nil
	}
	return 0, fmt.Errorf("%s does not belong to paramKey values", s)
}

// paramKeyValues returns all values of the enum
func paramKeyValues() []paramKey {
	return _paramKeyValues
}

// IsAparamKey returns "true" if the value is listed in the enum definition. "false" otherwise
func (i paramKey) IsAparamKey() bool {
	for _, v := range _paramKeyValues {
		if i == v {
			return true
		}
	}
	return false
}
