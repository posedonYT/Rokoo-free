package grpcserver

import (
	"context"

	mediav1 "rokko.dev/gen/media/v1"
	"rokko.dev/pkg/apperr"
	"rokko.dev/pkg/authn"
	"rokko.dev/pkg/id"
	"rokko.dev/services/media/internal/service"
)

type Handler struct {
	mediav1.UnimplementedMediaServiceServer
	svc *service.Service
}

func New(svc *service.Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) CreateUploadURL(ctx context.Context, req *mediav1.CreateUploadURLRequest) (*mediav1.CreateUploadURLResponse, error) {
	me, err := current(ctx)
	if err != nil {
		return nil, err
	}
	up, err := h.svc.CreateUpload(ctx, me, req.GetFilename(), req.GetContentType(), req.GetSize())
	if err != nil {
		return nil, err
	}
	return &mediav1.CreateUploadURLResponse{MediaId: id.Format(up.MediaID), UploadUrl: up.UploadURL}, nil
}

func (h *Handler) ConfirmUpload(ctx context.Context, req *mediav1.ConfirmUploadRequest) (*mediav1.Media, error) {
	me, err := current(ctx)
	if err != nil {
		return nil, err
	}
	mediaID, err := id.ParseID(req.GetMediaId())
	if err != nil {
		return nil, err
	}
	obj, err := h.svc.Confirm(ctx, me, mediaID)
	if err != nil {
		return nil, err
	}
	return &mediav1.Media{Id: id.Format(obj.ID), Filename: obj.Filename, ContentType: obj.ContentType, Size: obj.Size, Status: obj.Status}, nil
}

func (h *Handler) GetDownloadURL(ctx context.Context, req *mediav1.GetDownloadURLRequest) (*mediav1.GetDownloadURLResponse, error) {
	me, err := current(ctx)
	if err != nil {
		return nil, err
	}
	mediaID, err := id.ParseID(req.GetMediaId())
	if err != nil {
		return nil, err
	}
	url, err := h.svc.DownloadURL(ctx, me, mediaID)
	if err != nil {
		return nil, err
	}
	return &mediav1.GetDownloadURLResponse{Url: url}, nil
}

func current(ctx context.Context) (int64, error) {
	idnty, ok := authn.FromContext(ctx)
	if !ok {
		return 0, apperr.Unauthorized("missing user")
	}
	return idnty.UserID, nil
}
