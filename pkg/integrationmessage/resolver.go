package integrationmessage

import (
	"fmt"

	"github.com/go-jimu/components/ddd/message"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

func NewProtoPayloadResolver() message.PayloadResolver {
	return message.PayloadResolverFunc(func(kind message.Kind) (proto.Message, error) {
		typ, err := protoregistry.GlobalTypes.FindMessageByName(protoreflect.FullName(kind))
		if err != nil {
			return nil, fmt.Errorf("integration message payload %s: %w", kind, err)
		}
		return typ.New().Interface(), nil
	})
}
