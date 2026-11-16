package grpc

import (
	"context"
	"time"

	"api/internal/adapter/inbound/grpc/gen/classroomv1"
	"api/internal/core/domain"
	"api/internal/core/service"
)

// Forums of the classroom server

func toPost(p domain.ForumPost, lastRead *time.Time, me string) *classroomv1.Post {
	return &classroomv1.Post{Id: p.ID, ParentId: p.ParentID, AuthorUserId: p.AuthorUserID, AuthorName: p.AuthorName, AuthorRole: p.AuthorRole, Body: p.Body,
		Pinned: p.Pinned, Hidden: p.Hidden, HiddenReason: p.HiddenReason, HiddenByName: p.HiddenByName, CreatedAt: rfc3339(&p.CreatedAt),
		EditedAt: rfc3339(p.EditedAt), IsNew: p.AuthorUserID != me && (lastRead == nil || p.CreatedAt.After(*lastRead))}
}

func (s *classroomServer) GetForum(ctx context.Context, req *classroomv1.GetForumRequest) (*classroomv1.GetForumResponse, error) {
	v, err := s.classroom.Forum(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetItemId())
	if err != nil {
		return nil, s.fail("GetForum", err)
	}
	f := v.Forum
	out := &classroomv1.GetForumResponse{Forum: &classroomv1.Forum{ItemId: f.ItemID, RoomId: f.RoomID, Title: f.Title, Prompt: f.Prompt, Kind: f.Kind, Closed: f.Closed,
		ClosedByName: f.ClosedByName}, CanPost: v.CanPost, CanModerate: v.CanModerate, MyUserId: v.MyUserID, NewPosts: int32(v.NewPosts)}
	for _, t := range v.Threads {
		th := &classroomv1.Thread{Post: toPost(t.Post, v.LastRead, v.MyUserID)}
		for _, r := range t.Replies {
			th.Replies = append(th.Replies, toPost(r, v.LastRead, v.MyUserID))
		}
		out.Threads = append(out.Threads, th)
	}
	for _, p := range v.Participation {
		out.Participation = append(out.Participation, &classroomv1.Participation{StudentId: p.StudentID, StudentName: p.StudentName, StudentCode: p.StudentCode,
			Threads: int32(p.Threads), Replies: int32(p.Replies), LastAt: rfc3339(p.LastAt)})
	}
	return out, nil
}

func (s *classroomServer) SaveForum(ctx context.Context, req *classroomv1.SaveForumRequest) (*classroomv1.SaveForumResponse, error) {
	if err := s.classroom.SaveForum(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetItemId(), req.GetKind(), req.GetClosed()); err != nil {
		return nil, s.fail("SaveForum", err)
	}
	return &classroomv1.SaveForumResponse{}, nil
}

func (s *classroomServer) CreatePost(ctx context.Context, req *classroomv1.CreatePostRequest) (*classroomv1.CreatePostResponse, error) {
	p, err := s.classroom.Post(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetItemId(), req.GetParentId(), req.GetBody())
	if err != nil {
		return nil, s.fail("CreatePost", err)
	}
	return &classroomv1.CreatePostResponse{Post: toPost(p, &p.CreatedAt, p.AuthorUserID)}, nil
}

func (s *classroomServer) EditPost(ctx context.Context, req *classroomv1.EditPostRequest) (*classroomv1.EditPostResponse, error) {
	if err := s.classroom.EditPost(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), req.GetBody()); err != nil {
		return nil, s.fail("EditPost", err)
	}
	return &classroomv1.EditPostResponse{}, nil
}

func (s *classroomServer) DeletePost(ctx context.Context, req *classroomv1.DeletePostRequest) (*classroomv1.DeletePostResponse, error) {
	if err := s.classroom.DeletePost(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId()); err != nil {
		return nil, s.fail("DeletePost", err)
	}
	return &classroomv1.DeletePostResponse{}, nil
}

func (s *classroomServer) ModeratePost(ctx context.Context, req *classroomv1.ModeratePostRequest) (*classroomv1.ModeratePostResponse, error) {
	if err := s.classroom.ModeratePost(ctx, bearer(ctx), req.GetInstitutionSlug(), service.ModerateInput{PostID: req.GetId(), Pinned: req.GetPinned(), Hidden: req.GetHidden(),
		Reason: req.GetReason()}); err != nil {
		return nil, s.fail("ModeratePost", err)
	}
	return &classroomv1.ModeratePostResponse{}, nil
}
