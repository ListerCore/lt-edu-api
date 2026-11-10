package grpc

import (
	"context"
	"errors"

	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/timestamppb"

	"api/internal/adapter/inbound/grpc/gen/announcementsv1"
	"api/internal/core/domain"
	"api/internal/core/service"
)

// announcementServer exposes service.AnnouncementService reads to joseph-app's server
type announcementServer struct {
	announcementsv1.UnimplementedAnnouncementServiceServer
	announcements *service.AnnouncementService
	logger        zerolog.Logger
}

func (s *announcementServer) ListPublicAnnouncements(ctx context.Context, req *announcementsv1.ListPublicAnnouncementsRequest) (*announcementsv1.ListPublicAnnouncementsResponse, error) {
	items, err := s.announcements.PublicList(ctx, req.GetInstitutionSlug())
	if err != nil {
		return nil, s.fail("ListPublicAnnouncements", err)
	}
	return &announcementsv1.ListPublicAnnouncementsResponse{Announcements: toAnnouncements(items)}, nil
}

func (s *announcementServer) GetPublicAnnouncement(ctx context.Context, req *announcementsv1.GetPublicAnnouncementRequest) (*announcementsv1.GetPublicAnnouncementResponse, error) {
	a, err := s.announcements.PublicGet(ctx, req.GetSlug())
	if err != nil {
		return nil, s.fail("GetPublicAnnouncement", err)
	}
	return &announcementsv1.GetPublicAnnouncementResponse{Announcement: toAnnouncement(a)}, nil
}

func (s *announcementServer) ListAnnouncements(ctx context.Context, req *announcementsv1.ListAnnouncementsRequest) (*announcementsv1.ListAnnouncementsResponse, error) {
	feed, err := s.announcements.PanelList(ctx, bearer(ctx), req.GetInstitutionSlug())
	if err != nil {
		return nil, s.fail("ListAnnouncements", err)
	}
	return &announcementsv1.ListAnnouncementsResponse{Announcements: toAnnouncements(feed.Announcements), CanPublish: feed.CanPublish}, nil
}

func (s *announcementServer) GetAnnouncement(ctx context.Context, req *announcementsv1.GetAnnouncementRequest) (*announcementsv1.GetAnnouncementResponse, error) {
	a, editable, err := s.announcements.PanelGet(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetSlug())
	if err != nil {
		return nil, s.fail("GetAnnouncement", err)
	}
	return &announcementsv1.GetAnnouncementResponse{Announcement: toAnnouncement(a), CanEdit: editable}, nil
}

func (s *announcementServer) ListFeaturedAnnouncements(ctx context.Context, req *announcementsv1.ListFeaturedAnnouncementsRequest) (*announcementsv1.ListFeaturedAnnouncementsResponse, error) {
	items, err := s.announcements.PublicFeatured(ctx, req.GetInstitutionSlug())
	if err != nil {
		return nil, s.fail("ListFeaturedAnnouncements", err)
	}
	return &announcementsv1.ListFeaturedAnnouncementsResponse{Announcements: toAnnouncements(items)}, nil
}

func (s *announcementServer) fail(method string, err error) error {
	var de *domain.Error
	if !errors.As(err, &de) {
		s.logger.Error().Err(err).Str("method", method).Msg("announcement grpc call failed")
	}
	return fromDomain(err)
}

func toAnnouncements(items []domain.Announcement) []*announcementsv1.Announcement {
	out := make([]*announcementsv1.Announcement, len(items))
	for i, a := range items {
		out[i] = toAnnouncement(a)
	}
	return out
}

func toAnnouncement(a domain.Announcement) *announcementsv1.Announcement {
	attachments := make([]*announcementsv1.Attachment, len(a.Attachments))
	for i, att := range a.Attachments {
		attachments[i] = &announcementsv1.Attachment{Id: att.ID, Kind: att.Kind, FileName: att.FileName, ContentType: att.ContentType, SizeBytes: att.SizeBytes, Position: int32(att.Position)}
	}
	out := &announcementsv1.Announcement{
		Id: a.ID, Slug: a.Slug, Kind: a.Kind, Audience: a.Audience, Title: a.Title, Body: a.Body, IsPublic: a.IsPublic, Pinned: a.Pinned,
		AuthorName: a.AuthorName, PublishedAt: timestamppb.New(a.PublishedAt), InstitutionSlug: a.InstitutionSlug, Attachments: attachments,
		CtaLabel: a.CTALabel, CtaUrl: a.CTAURL, UpdatedAt: timestamppb.New(a.UpdatedAt),
	}
	if a.FeaturedFrom != nil && a.FeaturedUntil != nil {
		out.FeaturedFrom, out.FeaturedUntil = timestamppb.New(*a.FeaturedFrom), timestamppb.New(*a.FeaturedUntil)
	}
	return out
}
