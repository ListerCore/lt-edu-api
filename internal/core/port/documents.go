package port

import (
	"context"
	"time"

	"api/internal/core/domain"
)

// Printable documents: the services build these view models from their data and a DocumentRenderer turns them
// into PDF. Nothing here knows about storage; texts are already in Spanish and formatted for the page

// Image is a picture placed on a document (PNG or JPEG); empty Data means none
type Image struct {
	Data []byte
	// png | jpg
	Type string
}

// ---------- report card ----------

// CardTermDoc is one term of a report card: the tutor's conclusion and the attendance
type CardTermDoc struct {
	Name      string
	Comment   string
	Justified int
	Absent    int
	Late      int
	Conduct   *int
	// The term has not started for this card yet: its cells stay empty
	Empty bool
}

// CardCompetencyDoc is one row of the competencies grid
type CardCompetencyDoc struct {
	Name string
	// One level per term of the period ("" when there is none)
	Levels []string
	Annual string
}

// CardAreaDoc is an area with its competencies; transversal competencies come as their own area
type CardAreaDoc struct {
	Name            string
	Transversal     bool
	Competencies    []CardCompetencyDoc
	FinalConclusion string
}

// CardConclusionDoc is a descriptive conclusion of the selected term
type CardConclusionDoc struct {
	Competency string
	Text       string
}

// CardSubjectDoc is a row of a 0–20 or score card
type CardSubjectDoc struct {
	Name        string
	Teacher     string
	Evaluations string
	Average     string
	Grade       string
	Situation   string
	Failed      bool
}

// ReportCardDoc is one student's report card. Literal cards follow the SIAGIE layout; the others list subjects
type ReportCardDoc struct {
	Literal     bool
	Institution string
	Dre         string
	Ugel        string
	ModularCode string
	Logo        Image
	Year        string
	Period      string
	TermName    string
	Level       string
	Grade       string
	Section     string
	Student     string
	Code        string
	Document    string
	Tutor       string
	Provisional bool
	Terms       []CardTermDoc
	Areas       []CardAreaDoc
	// Number of the selected term (1-based) and its conclusions
	TermNumber  int
	Conclusions []CardConclusionDoc
	Subjects    []CardSubjectDoc
	SubjectHead string
	Passing     bool
	IssuedOn    time.Time
}

// ---------- timetables ----------

// Colour is an RGB fill
type Colour struct{ R, G, B int }

// GridCell is one slot: a course code and a detail line (teacher, section) on the course's colour; a cell with only
// a code prints it alone
type GridCell struct {
	Code   string
	Detail string
	Fill   *Colour
}

// GridRow is a row of a timetable grid: a class hour (or a section) or a break across the whole row
type GridRow struct {
	Head  string
	Break bool
	Cells []GridCell
}

// ScheduleGrid is one table of a page (a section's week, a teacher's week, a shift of the general timetable).
// With SubColumns every column (a day) splits in narrow ones (course and section, or one per section) and each row
// carries len(Columns)*len(SubColumns) cells
type ScheduleGrid struct {
	Caption    string
	Corner     string
	Columns    []string
	SubColumns []string
	Rows       []GridRow
}

// LegendRow is a line of a legend; Fills[i], when set, draws a colour chip before cell i
type LegendRow struct {
	Fills []*Colour
	Cells []string
}

// LegendTable explains the codes of a page
type LegendTable struct {
	Title   string
	Columns []string
	Rows    []LegendRow
	// Relative widths of the columns
	Widths []float64
}

// SchedulePage is one printed sheet: the grids side by side and the legends below them
type SchedulePage struct {
	Title    string
	Subtitle string
	Info     string
	Grids    []ScheduleGrid
	Legends  []LegendTable
}

// ScheduleDoc is a set of timetable sheets of one institution. It is also the content a version freezes, so it
// travels as JSON
type ScheduleDoc struct {
	Institution string
	// The institution's logo at the top left of every sheet (empty: none)
	Logo     Image
	Pages    []SchedulePage
	IssuedOn time.Time
	// The verification block printed at the foot of every sheet; nil prints none
	Seal *DocumentSeal `json:"-"`
}

// ---------- verification ----------

// SealSignature is a signature box: signed (date and code) or waiting
type SealSignature struct {
	Name     string
	Position string
	SignedAt *time.Time
	Code     string
}

// DocumentSeal is what makes a printed document verifiable: its version, the QR to its tracking page and the
// signatures. Mark, when set, crosses the sheet (VISTA PREVIA, PENDIENTE DE FIRMA, REEMPLAZADA, ANULADA)
type DocumentSeal struct {
	Version          int
	State            string
	Mark             string
	VerificationCode string
	TrackingCode     string
	TrackingURL      string
	// QR target: the tracking page pointing at this version
	QR         string
	Signatures []SealSignature
}

// ---------- acta ----------

// RecordDoc is an issued acta, as frozen when it was issued
type RecordDoc struct {
	Institution string
	Title       string
	Subtitle    string
	Code        string
	Issued      string
	Replaced    bool
	Info        []string
	// Column groups (areas or the unit) and their columns' short codes
	Groups []RecordGroupDoc
	Rows   []RecordRowDoc
	// What each column code means
	Columns    []string
	Summary    []string
	Legend     string
	Signatures []string
}

// RecordGroupDoc is a group of columns of an acta
type RecordGroupDoc struct {
	Name  string
	Codes []string
}

// RecordRowDoc is a student's line of an acta
type RecordRowDoc struct {
	N         int
	Code      string
	Name      string
	Note      string
	Cells     []string
	Situation string
}

// ---------- receipt ----------

// ReceiptLineDoc is a concept paid
type ReceiptLineDoc struct {
	Concept string
	Amount  string
}

// ReceiptDoc is an internal payment receipt
type ReceiptDoc struct {
	Institution string
	Logo        Image
	Number      string
	PaidOn      string
	Student     string
	Payer       string
	Method      string
	Lines       []ReceiptLineDoc
	Total       string
	ReceivedBy  string
	Annulled    bool
	AnnulReason string
}

// DocumentRenderer turns documents into PDF files
type DocumentRenderer interface {
	ReportCards(cards []ReportCardDoc) ([]byte, error)
	Schedules(doc ScheduleDoc) ([]byte, error)
	Record(doc RecordDoc) ([]byte, error)
	Receipt(doc ReceiptDoc) ([]byte, error)
	// Form is a portrait document of labelled fields in blocks (ficha única, acta de ubicación)
	Form(doc FormDoc) ([]byte, error)
	// Table is a list in landscape, one block per group with its header repeated (nómina de matrícula)
	Table(doc TableDoc) ([]byte, error)
}

// DocHeader is the heading of the official documents: the regional office, the UGEL, the institution and its codes
type DocHeader struct {
	Dre         string
	Ugel        string
	Institution string
	ModularCode string
	Logo        Image
	Title       string
	Subtitle    string
	// Top right: a number or code ("Acta N.° 3-2026")
	Code string
}

// FormDoc is a portrait form: blocks of label and value, free paragraphs, then signatures or the seal of an issued
// version
type FormDoc struct {
	Header     DocHeader
	Blocks     []FormBlock
	Paragraphs []string
	// Signature lines, when the document is not an issued version
	Signatures []string
	Seal       *DocumentSeal
	IssuedOn   time.Time
}

// FormBlock is a titled group of fields, printed in Columns columns (1 or 2)
type FormBlock struct {
	Title   string
	Columns int
	Fields  []FormField
}

type FormField struct {
	Label string
	Value string
}

// TableDoc is a landscape list: columns (with relative widths), groups of rows each starting a page, totals
type TableDoc struct {
	Header     DocHeader
	Columns    []string
	Widths     []float64
	Groups     []TableGroup
	Legend     string
	IssuedOn   time.Time
	Signatures []string
}

type TableGroup struct {
	Title string
	Info  []string
	Rows  [][]string
	Total string
}

// SignerCandidate is a staff member who can be named signer
type SignerCandidate struct {
	StaffID  string
	Name     string
	Position string
}

// DocumentRepository stores the issued documents, their versions, signatures and history (documents schema)
type DocumentRepository interface {
	Signers(ctx context.Context, institutionID, kind string) ([]domain.DocumentSigner, error)
	// SetSigners replaces the signers of a kind, in the order given, and updates the signatures the pending versions
	// still wait for; it returns the numbers of the versions that got published by it
	SetSigners(ctx context.Context, institutionID, kind string, signers []domain.DocumentSigner, actor string, at time.Time) ([]int, error)
	Candidates(ctx context.Context, institutionID string) ([]SignerCandidate, error)

	// Documents of a kind whose scope starts with prefix (a period's timetables)
	Documents(ctx context.Context, institutionID, kind, prefix string) ([]domain.IssuedDocument, error)
	DocumentByTracking(ctx context.Context, tracking string) (domain.IssuedDocument, error)
	// Versions newest first, with signatures and without content
	Versions(ctx context.Context, documentID string) ([]domain.DocumentVersion, error)
	// Version with its content and signatures, by id inside an institution
	Version(ctx context.Context, institutionID, id string) (domain.IssuedDocument, domain.DocumentVersion, error)
	VersionByNumber(ctx context.Context, documentID string, number int) (domain.DocumentVersion, error)
	Events(ctx context.Context, documentID string) ([]domain.DocumentEvent, error)

	// CreateVersion opens the document when it is new (doc.TrackingCode is used then) and adds a pending version
	// with the signatures given; pending versions before it are cancelled. When the newest live version has the same
	// hash nothing is added and that one comes back with created false
	CreateVersion(ctx context.Context, doc domain.IssuedDocument, v domain.DocumentVersion, userID string) (domain.IssuedDocument, domain.DocumentVersion, bool, error)
	// Sign puts a signature (by its sort) on a pending version; with the last one the version is published and the
	// previous published one superseded. It reports whether the version got published
	Sign(ctx context.Context, versionID string, sort int, userID, code string, at time.Time) (bool, error)
}

// ---------- spreadsheets ----------

// SheetCell is a cell of a spreadsheet table
type SheetCell struct {
	Text string
	Fill *Colour
	Bold bool
}

// SheetTable is a titled table of a sheet; several stack down with a blank row between them
type SheetTable struct {
	Title   string
	Columns []string
	Rows    [][]SheetCell
}

// Sheet is a tab of a workbook; Filter turns on the filter of its first table
type Sheet struct {
	Name   string
	Widths []float64
	Tables []SheetTable
	Filter bool
}

// Workbook is a spreadsheet file
type Workbook struct {
	Title  string
	Sheets []Sheet
}

// WorkbookRenderer turns workbooks into .xlsx files
type WorkbookRenderer interface {
	Workbook(book Workbook) ([]byte, error)
}
