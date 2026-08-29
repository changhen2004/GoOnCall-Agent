package dto

// ApprovalActionRequest 是批准 / 拒绝审批的请求体。
type ApprovalActionRequest struct {
	ApprovedBy string `json:"approved_by"`
	Comment    string `json:"comment"`
}
