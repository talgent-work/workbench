package command

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
)

type SendWorkMessage struct {
	ActorID, WorkID string
	Input           ExecutionInput
}

type SendWorkMessageHandler struct {
	authorization access.WorkAuthorizer
	input         *DeliverWorkInputHandler
}

func NewSendWorkMessageHandler(authorization access.WorkAuthorizer, input *DeliverWorkInputHandler) *SendWorkMessageHandler {
	return &SendWorkMessageHandler{authorization: authorization, input: input}
}

func (h *SendWorkMessageHandler) Handle(ctx context.Context, request SendWorkMessage) error {
	subject := request.ActorID
	if err := h.authorization.AuthorizeWork(ctx, subject, request.WorkID); err != nil {
		return err
	}
	_, err := h.input.Deliver(ctx, DeliverWorkInput{SubjectID: subject, WorkID: request.WorkID, Input: request.Input})
	return err
}

type ReplyToWorkInteraction struct {
	ActorID, WorkID string
	Response        InteractionResponse
}

type ReplyToWorkInteractionHandler struct {
	authorization access.WorkAuthorizer
	interaction   *RespondWorkInteractionHandler
}

func NewReplyToWorkInteractionHandler(authorization access.WorkAuthorizer, interaction *RespondWorkInteractionHandler) *ReplyToWorkInteractionHandler {
	return &ReplyToWorkInteractionHandler{authorization: authorization, interaction: interaction}
}

func (h *ReplyToWorkInteractionHandler) Handle(ctx context.Context, request ReplyToWorkInteraction) error {
	subject := request.ActorID
	if err := h.authorization.AuthorizeWork(ctx, subject, request.WorkID); err != nil {
		return err
	}
	return h.interaction.Handle(ctx, RespondWorkInteraction{SubjectID: subject, WorkID: request.WorkID, Response: request.Response})
}
