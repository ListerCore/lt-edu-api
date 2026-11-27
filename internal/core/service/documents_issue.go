package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// Issued documents: a role that manages a document generates a version (frozen content, verification code, the
// signatures it needs); the signers sign it and with the last signature it is published, replacing the previous
// published version. Every printed sheet carries a QR to the public tracking page, so anyone can check whether
// the paper in their hands is the current version

// trackingAlphabet avoids letters that read alike (no i, l, o, u)
const trackingAlphabet = "0123456789abcdefghjkmnpqrstvwxyz"

// randomCode is n characters of an alphabet, from crypto/rand
func randomCode(alphabet string, n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

// verificationCode is XXXX-XXXX-XXXX in hexadecimal
func verificationCode() string {
	c := strings.ToUpper(randomCode("0123456789ABCDEF", 12))
	return c[:4] + "-" + c[4:8] + "-" + c[8:]
}

// TrackingURL is the public page of a document; with a code it points at one version
func (s *DocumentService) TrackingURL(tracking, code string) string {
	u := strings.TrimRight(s.siteURL, "/") + "/seguimiento/" + tracking
	if code != "" {
		u += "?codigo=" + code
	}
	return u
}

// VersionStates in Spanish, for the seal and the screens
var versionStates = map[string]string{domain.VersionPending: "Pendiente de firma", domain.VersionPublished: "Vigente", domain.VersionSuperseded: "Reemplazada", domain.VersionCancelled: "Anulada"}

// versionMarks cross the sheets that are not the current one
var versionMarks = map[string]string{domain.VersionPending: "PENDIENTE DE FIRMA", domain.VersionSuperseded: "REEMPLAZADA", domain.VersionCancelled: "ANULADA"}

// seal is the verification block of a version
func (s *DocumentService) seal(d domain.IssuedDocument, v domain.DocumentVersion) *port.DocumentSeal {
	out := &port.DocumentSeal{Version: v.Number, State: versionStates[v.Status], Mark: versionMarks[v.Status], VerificationCode: v.VerificationCode,
		TrackingCode: d.TrackingCode, TrackingURL: s.TrackingURL(d.TrackingCode, ""), QR: s.TrackingURL(d.TrackingCode, v.VerificationCode)}
	for _, sg := range v.Signatures {
		out.Signatures = append(out.Signatures, port.SealSignature{Name: sg.Name, Position: sg.Position, SignedAt: sg.SignedAt, Code: sg.Code})
	}
	return out
}

// contentHash identifies a content regardless of the day it was generated
func contentHash(doc port.ScheduleDoc) (string, []byte, error) {
	body, err := json.Marshal(doc)
	if err != nil {
		return "", nil, err
	}
	doc.IssuedOn = time.Time{}
	plain, err := json.Marshal(doc)
	if err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(plain)
	return hex.EncodeToString(sum[:]), body, nil
}

// actorName is the viewer's name as the history shows it
func (s *DocumentService) actorName(ctx context.Context, token string) string {
	me, err := s.assessment.academic.auth.Principal(ctx, token)
	if err != nil {
		return ""
	}
	if me.User.FullName != "" {
		return me.User.FullName
	}
	return me.User.Email
}

// ---------- signers ----------

// maxSigners caps the signatures a document asks for
const maxSigners = 10

// SignerSetup is who signs a kind of document and who could
type SignerSetup struct {
	Signers    []domain.DocumentSigner
	Candidates []port.SignerCandidate
	CanManage  bool
}

// signerScope is who reads and who manages the signers of a kind: the timetables go with teaching, the actas de
// ubicación with the secretariat (EBA only)
func (s *DocumentService) signerScope(ctx context.Context, token, slug, kind string, write bool) (academicScope, bool, error) {
	switch kind {
	case "", domain.DocumentSchedules:
		sc, err := s.teaching.scope(ctx, token, slug, write)
		return sc, teachingManagers[sc.role], err
	case domain.DocumentPlacement:
		if s.people == nil {
			return academicScope{}, false, domain.ErrNotFound
		}
		sc, err := s.people.ebaScope(ctx, token, slug, write)
		return sc, peopleManagers[sc.role], err
	case domain.DocumentCertificate:
		if s.people == nil {
			return academicScope{}, false, domain.ErrNotFound
		}
		sc, err := s.people.ebaScope(ctx, token, slug, write)
		return sc, peopleManagers[sc.role], err
	case domain.DocumentEvaluationRecord:
		sc, err := s.closing.ebaScope(ctx, token, slug, write)
		return sc.academicScope, sc.manager(), err
	}
	return academicScope{}, false, domain.Validation(map[string]string{"kind": "Tipo de documento desconocido."})
}

func signerKind(kind string) string {
	if kind == "" {
		return domain.DocumentSchedules
	}
	return kind
}

// Signers of a kind of document (the timetables when empty); the staff list comes along for the managers to pick from
func (s *DocumentService) Signers(ctx context.Context, token, slug, kind string) (SignerSetup, error) {
	sc, canManage, err := s.signerScope(ctx, token, slug, kind, false)
	if err != nil {
		return SignerSetup{}, err
	}
	out := SignerSetup{CanManage: canManage}
	if out.Signers, err = s.docs.Signers(ctx, sc.institution.ID, signerKind(kind)); err != nil {
		return out, err
	}
	if out.CanManage {
		out.Candidates, err = s.docs.Candidates(ctx, sc.institution.ID)
	}
	return out, err
}

// SetSigners replaces who signs a kind of document, in order; only the roles that manage it
func (s *DocumentService) SetSigners(ctx context.Context, token, slug, kind string, signers []domain.DocumentSigner) ([]domain.DocumentSigner, error) {
	sc, _, err := s.signerScope(ctx, token, slug, kind, true)
	if err != nil {
		return nil, err
	}
	switch {
	case len(signers) == 0:
		return nil, domain.Validation(map[string]string{"signers": "Deja al menos un firmante."})
	case len(signers) > maxSigners:
		return nil, domain.Validation(map[string]string{"signers": fmt.Sprintf("Hasta %d firmantes.", maxSigners)})
	}
	seen := map[string]bool{}
	for i := range signers {
		signers[i].Position = strings.TrimSpace(signers[i].Position)
		switch {
		case signers[i].StaffID == "":
			return nil, domain.Validation(map[string]string{"signers": "Elige a la persona de cada firma."})
		case seen[signers[i].StaffID]:
			return nil, domain.Validation(map[string]string{"signers": "Una persona firma una sola vez."})
		case signers[i].Position == "" || len([]rune(signers[i].Position)) > 120:
			return nil, domain.Validation(map[string]string{"signers": "Escribe el cargo de cada firmante (hasta 120 caracteres)."})
		}
		seen[signers[i].StaffID] = true
	}
	if _, err := s.docs.SetSigners(ctx, sc.institution.ID, signerKind(kind), signers, s.actorName(ctx, token), s.now()); err != nil {
		return nil, err
	}
	s.teaching.changed(ctx, sc, "documents")
	return s.docs.Signers(ctx, sc.institution.ID, signerKind(kind))
}

// ---------- previews ----------

// SchedulesPreview is the PDF of the timetables as they are now, without a version: crossed as a preview and with
// the signature boxes of the current signers still empty
func (s *DocumentService) SchedulesPreview(ctx context.Context, token, slug, periodID, mode, target string) (PdfFile, error) {
	b, err := s.buildSchedules(ctx, token, slug, periodID, mode, target)
	if err != nil {
		return PdfFile{}, err
	}
	sc, err := s.teaching.scope(ctx, token, slug, false)
	if err != nil {
		return PdfFile{}, err
	}
	signers, err := s.docs.Signers(ctx, sc.institution.ID, domain.DocumentSchedules)
	if err != nil {
		return PdfFile{}, err
	}
	seal := &port.DocumentSeal{State: "Vista previa", Mark: "VISTA PREVIA"}
	for _, sg := range signers {
		seal.Signatures = append(seal.Signatures, port.SealSignature{Name: sg.Name, Position: sg.Position})
	}
	b.doc.Seal = seal
	content, err := s.render.Schedules(b.doc)
	if err != nil {
		return PdfFile{}, err
	}
	return PdfFile{Name: b.file + "-vista-previa.pdf", Content: content}, nil
}

// ---------- versions ----------

// IssuedVersions is a publication with its versions and history
type IssuedVersions struct {
	Document domain.IssuedDocument
	Versions []domain.DocumentVersion
	Events   []domain.DocumentEvent
}

// ScheduleIssues is the publish panel: the period's publications, the current signers and what the viewer can do
type ScheduleIssues struct {
	Items     []IssuedVersions
	Signers   []domain.DocumentSigner
	CanManage bool
	// The viewer's staff record, to show "Firmar" on the signatures waiting for them
	MyStaffID string
}

func scheduleScope(periodID, mode, target string) string { return periodID + ":" + mode + ":" + target }

// ScheduleIssues lists the timetable publications of a period, newest version first
func (s *DocumentService) ScheduleIssues(ctx context.Context, token, slug, periodID string) (ScheduleIssues, error) {
	sc, err := s.teaching.scope(ctx, token, slug, false)
	if err != nil {
		return ScheduleIssues{}, err
	}
	out := ScheduleIssues{CanManage: teachingManagers[sc.role]}
	if me, err := s.assessment.academic.auth.Principal(ctx, token); err == nil {
		if st, err := s.staff.StaffByEmail(ctx, sc.institution.ID, me.User.Email); err == nil {
			out.MyStaffID = st.ID
		}
	}
	if out.Signers, err = s.docs.Signers(ctx, sc.institution.ID, domain.DocumentSchedules); err != nil {
		return out, err
	}
	docs, err := s.docs.Documents(ctx, sc.institution.ID, domain.DocumentSchedules, periodID+":")
	if err != nil {
		return out, err
	}
	for _, d := range docs {
		item := IssuedVersions{Document: d}
		if item.Versions, err = s.docs.Versions(ctx, d.ID); err != nil {
			return out, err
		}
		if item.Events, err = s.docs.Events(ctx, d.ID); err != nil {
			return out, err
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

// GenerateSchedules freezes the timetables of a mode and target as a new version waiting for signatures. When
// nothing changed since the newest live version, that version comes back and nothing is added
func (s *DocumentService) GenerateSchedules(ctx context.Context, token, slug, periodID, mode, target, note string) (domain.IssuedDocument, domain.DocumentVersion, bool, error) {
	sc, err := s.teaching.scope(ctx, token, slug, true)
	if err != nil {
		if errors.Is(err, domain.ErrAcademicReadOnly) {
			err = domain.ErrDocumentsDenied
		}
		return domain.IssuedDocument{}, domain.DocumentVersion{}, false, err
	}
	if note = strings.TrimSpace(note); len([]rune(note)) > 300 {
		return domain.IssuedDocument{}, domain.DocumentVersion{}, false, domain.Validation(map[string]string{"note": "La nota va hasta 300 caracteres."})
	}
	signers, err := s.docs.Signers(ctx, sc.institution.ID, domain.DocumentSchedules)
	if err != nil {
		return domain.IssuedDocument{}, domain.DocumentVersion{}, false, err
	}
	if len(signers) == 0 {
		return domain.IssuedDocument{}, domain.DocumentVersion{}, false, domain.ErrNoSigners
	}
	b, err := s.buildSchedules(ctx, token, slug, periodID, mode, target)
	if err != nil {
		return domain.IssuedDocument{}, domain.DocumentVersion{}, false, err
	}
	hash, body, err := contentHash(b.doc)
	if err != nil {
		return domain.IssuedDocument{}, domain.DocumentVersion{}, false, err
	}
	v := domain.DocumentVersion{VerificationCode: verificationCode(), Content: body, ContentHash: hash, Note: note, CreatedName: s.actorName(ctx, token)}
	for i, sg := range signers {
		v.Signatures = append(v.Signatures, domain.DocumentSignature{Sort: i + 1, StaffID: sg.StaffID, Name: sg.Name, Position: sg.Position})
	}
	doc := domain.IssuedDocument{InstitutionID: sc.institution.ID, Kind: domain.DocumentSchedules, ScopeKey: scheduleScope(periodID, mode, target),
		Title: b.title, TrackingCode: randomCode(trackingAlphabet, 12)}
	d, created, fresh, err := s.docs.CreateVersion(ctx, doc, v, sc.userID)
	if err == nil {
		s.teaching.changed(ctx, sc, "documents")
	}
	return d, created, fresh, err
}

// SignVersion puts the viewer's signature on a pending version. The viewer signs as the staff member their email
// belongs to, on the first signature of theirs still empty
func (s *DocumentService) SignVersion(ctx context.Context, token, slug, versionID string) (domain.DocumentVersion, bool, error) {
	sc, err := resolveInstitution(ctx, s.assessment.academic.auth, s.assessment.academic.repo.InstitutionBySlug, token, slug)
	if err != nil {
		return domain.DocumentVersion{}, false, err
	}
	me, err := s.assessment.academic.auth.Principal(ctx, token)
	if err != nil {
		return domain.DocumentVersion{}, false, err
	}
	st, err := s.staff.StaffByEmail(ctx, sc.institution.ID, me.User.Email)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.DocumentVersion{}, false, domain.ErrNotSigner
	}
	if err != nil {
		return domain.DocumentVersion{}, false, err
	}
	_, v, err := s.docs.Version(ctx, sc.institution.ID, versionID)
	if err != nil {
		return v, false, err
	}
	if v.Status != domain.VersionPending {
		return v, false, domain.ErrVersionClosed
	}
	i := slices.IndexFunc(v.Signatures, func(sg domain.DocumentSignature) bool { return sg.StaffID == st.ID && sg.SignedAt == nil })
	if i < 0 {
		return v, false, domain.ErrNotSigner
	}
	published, err := s.docs.Sign(ctx, versionID, v.Signatures[i].Sort, sc.userID, verificationCode(), s.now())
	if err != nil {
		return v, false, err
	}
	s.assessment.academic.changed(ctx, academicScope{institutionScope: sc}, "documents")
	_, v, err = s.docs.Version(ctx, sc.institution.ID, versionID)
	return v, published, err
}

// renderVersion prints a frozen version with its seal as it stands now
func (s *DocumentService) renderVersion(d domain.IssuedDocument, v domain.DocumentVersion) (PdfFile, error) {
	switch d.Kind {
	case domain.DocumentPlacement, domain.DocumentCertificate:
		return s.renderForm(d, v)
	case domain.DocumentEvaluationRecord:
		return s.renderRecord(d, v)
	}
	var doc port.ScheduleDoc
	if err := json.Unmarshal(v.Content, &doc); err != nil {
		return PdfFile{}, fmt.Errorf("version %s: %w", v.ID, err)
	}
	doc.IssuedOn = v.CreatedAt.In(lima())
	doc.Seal = s.seal(d, v)
	content, err := s.render.Schedules(doc)
	if err != nil {
		return PdfFile{}, err
	}
	return PdfFile{Name: fmt.Sprintf("%s-v%d.pdf", slugify(d.Title), v.Number), Content: content}, nil
}

// VersionPdf is the PDF of one version, for the staff that can read the timetables
func (s *DocumentService) VersionPdf(ctx context.Context, token, slug, versionID string) (PdfFile, error) {
	sc, err := s.teaching.scope(ctx, token, slug, false)
	if err != nil {
		return PdfFile{}, err
	}
	d, v, err := s.docs.Version(ctx, sc.institution.ID, versionID)
	if err != nil {
		return PdfFile{}, err
	}
	return s.renderVersion(d, v)
}

// ---------- public tracking ----------

// Tracking is what the QR opens: the document, every version and its history. Matched is the version whose code
// was given (0 when none or unknown)
type Tracking struct {
	Document domain.IssuedDocument
	Versions []domain.DocumentVersion
	Events   []domain.DocumentEvent
	Matched  int
}

// Track looks a document up by its tracking code, without a session; the version of a verification code, when
// given, is pointed out
func (s *DocumentService) Track(ctx context.Context, tracking, code string) (Tracking, error) {
	if len(tracking) != 12 || strings.Trim(strings.ToLower(tracking), trackingAlphabet) != "" {
		return Tracking{}, domain.ErrNotFound
	}
	d, err := s.docs.DocumentByTracking(ctx, strings.ToLower(tracking))
	if err != nil {
		return Tracking{}, err
	}
	out := Tracking{Document: d}
	if out.Versions, err = s.docs.Versions(ctx, d.ID); err != nil {
		return out, err
	}
	if out.Events, err = s.docs.Events(ctx, d.ID); err != nil {
		return out, err
	}
	code = strings.ToUpper(strings.TrimSpace(code))
	for _, v := range out.Versions {
		if code != "" && (v.VerificationCode == code || slices.ContainsFunc(v.Signatures, func(sg domain.DocumentSignature) bool { return sg.Code == code })) {
			out.Matched = v.Number
		}
	}
	// The page is public: the codes are what proves a sheet (who holds it already has them) and the notes are
	// internal, so neither goes out
	for i := range out.Versions {
		out.Versions[i].VerificationCode, out.Versions[i].Note = "", ""
		for j := range out.Versions[i].Signatures {
			out.Versions[i].Signatures[j].Code = ""
		}
	}
	for i := range out.Events {
		out.Events[i].Note = ""
	}
	return out, nil
}

// TrackPdf is the PDF of a version that was published (current or superseded), without a session
func (s *DocumentService) TrackPdf(ctx context.Context, tracking string, number int) (PdfFile, error) {
	t, err := s.Track(ctx, tracking, "")
	if err != nil {
		return PdfFile{}, err
	}
	v, err := s.docs.VersionByNumber(ctx, t.Document.ID, number)
	if err != nil {
		return PdfFile{}, err
	}
	if v.Status != domain.VersionPublished && v.Status != domain.VersionSuperseded {
		return PdfFile{}, domain.ErrNotFound
	}
	return s.renderVersion(t.Document, v)
}
