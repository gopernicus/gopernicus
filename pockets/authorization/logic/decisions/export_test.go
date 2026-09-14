package decisions

// Test-only seams let tests use the memory adapter without an import cycle.
type RoleEngineForTest = roleEngine
type KindForTest = kind

var NewCompositeForTest = newComposite
var NewRoleEngineForTest = newRoleEngine
var MemoRoleReadsForTest = memoRoleReads
var RoleCheckForTest = (*roleEngine).check
var CheckWithForTest = (*Service).check
var CheckExplainWithForTest = (*Service).checkExplain
var CheckBatchWithForTest = (*Service).checkBatch
