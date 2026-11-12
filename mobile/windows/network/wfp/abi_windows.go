//go:build windows && (amd64 || arm64)

package wfp

import "unsafe"

// Compile-time assertions against the upstream Windows SDK ABI sizes/offsets.
// These guard both cross-builds even before a Windows execution host is ready.
var (
	_ [unsafe.Sizeof(wtFwpByteBlob{}) - wtFwpByteBlob_Size]byte
	_ [wtFwpByteBlob_Size - unsafe.Sizeof(wtFwpByteBlob{})]byte
	_ [unsafe.Sizeof(wtFwpConditionValue0{}) - wtFwpConditionValue0_Size]byte
	_ [wtFwpConditionValue0_Size - unsafe.Sizeof(wtFwpConditionValue0{})]byte
	_ [unsafe.Sizeof(wtFwpmFilter0{}) - wtFwpmFilter0_Size]byte
	_ [wtFwpmFilter0_Size - unsafe.Sizeof(wtFwpmFilter0{})]byte
	_ [unsafe.Offsetof(wtFwpmFilter0{}.filterCondition) - wtFwpmFilter0_filterCondition_Offset]byte
	_ [wtFwpmFilter0_filterCondition_Offset - unsafe.Offsetof(wtFwpmFilter0{}.filterCondition)]byte
	_ [unsafe.Sizeof(wtFwpmFilterCondition0{}) - wtFwpmFilterCondition0_Size]byte
	_ [wtFwpmFilterCondition0_Size - unsafe.Sizeof(wtFwpmFilterCondition0{})]byte
	_ [unsafe.Sizeof(wtFwpmSession0{}) - wtFwpmSession0_Size]byte
	_ [wtFwpmSession0_Size - unsafe.Sizeof(wtFwpmSession0{})]byte
)
