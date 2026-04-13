package ws

// Event type constants for WebSocket messages.
const (
	EventListItemChecked  = "list.item.checked"
	EventListItemAdded    = "list.item.added"
	EventListItemRemoved  = "list.item.removed"
	EventReceiptProcessing = "receipt.processing"
	EventReceiptComplete  = "receipt.complete"
	EventReceiptMatched   = "receipt.matched"
	EventProductUpdated   = "product.updated"
)

// Message represents a WebSocket message broadcast to household members.
type Message struct {
	Type      string      `json:"type"`
	Household string      `json:"household"`
	Payload   interface{} `json:"payload"`
}
