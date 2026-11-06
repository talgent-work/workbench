package claudecode

import (
	"unicode/utf8"

	dispatcherpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/public/v1"
)

func withDetail(message *dispatcherpublicv1.UnifiedMessage, detail string, format dispatcherpublicv1.MessageDetailFormat) *dispatcherpublicv1.UnifiedMessage {
	message.Detail = detail
	message.DetailFormat = format
	message.DetailLength = uint32(utf8.RuneCountInString(detail))
	return message
}
