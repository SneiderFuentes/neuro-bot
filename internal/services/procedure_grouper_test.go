package services

import (
	"context"
	"testing"

	"github.com/neuro-bot/neuro-bot/internal/domain"
)

// ---------------------------------------------------------------------------
// Mock ProcedureRepository
// ---------------------------------------------------------------------------

type mockProcRepo struct {
	procs map[string]*domain.Procedure
}

func (m *mockProcRepo) FindByCode(ctx context.Context, code string) (*domain.Procedure, error) {
	if p, ok := m.procs[code]; ok {
		return p, nil
	}
	return nil, nil
}
func (m *mockProcRepo) FindByID(ctx context.Context, id int) (*domain.Procedure, error) {
	return nil, nil
}
func (m *mockProcRepo) SearchByName(ctx context.Context, name string) ([]domain.Procedure, error) {
	return nil, nil
}
func (m *mockProcRepo) FindAllActive(ctx context.Context) ([]domain.Procedure, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newMock(entries ...struct {
	code    string
	name    string
	service string
	spaces  int
}) *mockProcRepo {
	m := &mockProcRepo{procs: make(map[string]*domain.Procedure)}
	for _, e := range entries {
		m.procs[e.code] = &domain.Procedure{
			Code:           e.code,
			Name:           e.name,
			ServiceName:    e.service,
			RequiredSpaces: e.spaces,
		}
	}
	return m
}

func cup(code, name string, qty int) CUPSEntry {
	return CUPSEntry{Code: code, Name: name, Quantity: qty}
}

func findGroup(groups []CUPSGroup, svc string) *CUPSGroup {
	for i := range groups {
		if groups[i].ServiceType == svc {
			return &groups[i]
		}
	}
	return nil
}

func findCup(cups []CUPSEntry, code string) *CUPSEntry {
	for i := range cups {
		if cups[i].Code == code {
			return &cups[i]
		}
	}
	return nil
}

// ===========================================================================
// GroupByServiceFromDB tests
// ===========================================================================

// 1. Empty input produces a single "General" group with Espacios=1.
func TestGroupByServiceFromDB_Empty(t *testing.T) {
	mock := newMock()
	groups, err := GroupByServiceFromDB(context.Background(), nil, mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if groups[0].ServiceType != "General" {
		t.Errorf("expected service 'General', got %q", groups[0].ServiceType)
	}
	if groups[0].Espacios != 1 {
		t.Errorf("expected Espacios=1, got %d", groups[0].Espacios)
	}
	if len(groups[0].Cups) != 0 {
		t.Errorf("expected 0 cups, got %d", len(groups[0].Cups))
	}
}

// 2. Single CUPS found in DB (non-Fisiatria service) gets correct group.
func TestGroupByServiceFromDB_SingleCup(t *testing.T) {
	mock := newMock(struct {
		code, name, service string
		spaces              int
	}{"890271", "RESONANCIA CEREBRAL", "Resonancia", 2})

	cups := []CUPSEntry{cup("890271", "RM Cerebral OCR", 1)}
	groups, err := GroupByServiceFromDB(context.Background(), cups, mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	g := groups[0]
	if g.ServiceType != "Resonancia" {
		t.Errorf("expected service 'Resonancia', got %q", g.ServiceType)
	}
	if g.Espacios != 2 {
		t.Errorf("expected Espacios=2 (1 qty * 2 spaces), got %d", g.Espacios)
	}
	if len(g.Cups) != 1 {
		t.Fatalf("expected 1 cup, got %d", len(g.Cups))
	}
	// Name should be enriched from DB
	if g.Cups[0].Name != "RESONANCIA CEREBRAL" {
		t.Errorf("expected enriched name 'RESONANCIA CEREBRAL', got %q", g.Cups[0].Name)
	}
}

// 3. CUPS code not found in DB falls back to "General" with Espacios=1.
func TestGroupByServiceFromDB_SingleCup_NotInDB(t *testing.T) {
	mock := newMock() // empty DB

	cups := []CUPSEntry{cup("999999", "Unknown Procedure", 1)}
	groups, err := GroupByServiceFromDB(context.Background(), cups, mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	g := groups[0]
	if g.ServiceType != "General" {
		t.Errorf("expected service 'General', got %q", g.ServiceType)
	}
	if g.Espacios != 1 {
		t.Errorf("expected Espacios=1, got %d", g.Espacios)
	}
}

// 4. Multiple CUPS of the same service are merged into one group with summed spaces.
func TestGroupByServiceFromDB_MultipleSameService(t *testing.T) {
	mock := newMock(
		struct {
			code, name, service string
			spaces              int
		}{"883101", "RM CEREBRAL SIMPLE", "Resonancia", 2},
		struct {
			code, name, service string
			spaces              int
		}{"883201", "RM COLUMNA CERVICAL", "Resonancia", 2},
	)

	cups := []CUPSEntry{
		cup("883101", "RM cerebral", 1),
		cup("883201", "RM columna", 1),
	}
	groups, err := GroupByServiceFromDB(context.Background(), cups, mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	g := groups[0]
	if g.ServiceType != "Resonancia" {
		t.Errorf("expected service 'Resonancia', got %q", g.ServiceType)
	}
	if g.Espacios != 4 {
		t.Errorf("expected Espacios=4 (2+2), got %d", g.Espacios)
	}
	if len(g.Cups) != 2 {
		t.Errorf("expected 2 cups, got %d", len(g.Cups))
	}
}

// 5. CUPS from different services produce separate groups.
func TestGroupByServiceFromDB_MultipleDiffService(t *testing.T) {
	mock := newMock(
		struct {
			code, name, service string
			spaces              int
		}{"883101", "RM CEREBRAL", "Resonancia", 2},
		struct {
			code, name, service string
			spaces              int
		}{"890205", "CONSULTA NEUROLOGIA", "Neurologia", 1},
	)

	cups := []CUPSEntry{
		cup("883101", "RM cerebral", 1),
		cup("890205", "Consulta neuro", 1),
	}
	groups, err := GroupByServiceFromDB(context.Background(), cups, mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}

	resGrp := findGroup(groups, "Resonancia")
	if resGrp == nil {
		t.Fatal("missing 'Resonancia' group")
	}
	neuroGrp := findGroup(groups, "Neurologia")
	if neuroGrp == nil {
		t.Fatal("missing 'Neurologia' group")
	}
}

// 6. DB name enrichment overwrites OCR-extracted name.
func TestGroupByServiceFromDB_EnrichesName(t *testing.T) {
	mock := newMock(struct {
		code, name, service string
		spaces              int
	}{"890271", "ELECTROMIOGRAFIA DE 2 EXTREMIDADES", "Fisiatria", 1})

	cups := []CUPSEntry{cup("890271", "Electromiografia OCR name", 1)}
	groups, err := GroupByServiceFromDB(context.Background(), cups, mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groups) < 1 {
		t.Fatal("expected at least 1 group")
	}
	g := groups[0]
	if len(g.Cups) == 0 {
		t.Fatal("expected at least 1 cup in group")
	}
	if g.Cups[0].Name != "ELECTROMIOGRAFIA DE 2 EXTREMIDADES" {
		t.Errorf("expected enriched name from DB, got %q", g.Cups[0].Name)
	}
}

// 7. Quantity acts as multiplier for RequiredSpaces.
func TestGroupByServiceFromDB_QuantityMultiplier(t *testing.T) {
	mock := newMock(struct {
		code, name, service string
		spaces              int
	}{"883101", "RM CEREBRAL", "Resonancia", 2})

	cups := []CUPSEntry{cup("883101", "RM cerebral", 3)} // Quantity=3
	groups, err := GroupByServiceFromDB(context.Background(), cups, mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if groups[0].Espacios != 6 {
		t.Errorf("expected Espacios=6 (3 qty * 2 spaces), got %d", groups[0].Espacios)
	}
}

// ===========================================================================
// Fisiatria rules tests (applyFisiatriaRules)
// ===========================================================================

// 8. EMG code without NC: NC (891509) is auto-added with Qty = totalEMG * 4.
//    2 EMG (<=3) -> Espacios=1.
func TestFisiatria_EMGWithoutNC(t *testing.T) {
	mock := newMock(struct {
		code, name, service string
		spaces              int
	}{"29120", "ELECTROMIOGRAFIA", "Fisiatria", 1})

	cups := []CUPSEntry{cup("29120", "EMG", 2)}
	groups, err := GroupByServiceFromDB(context.Background(), cups, mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g := findGroup(groups, "Fisiatria")
	if g == nil {
		t.Fatal("missing 'Fisiatria' group")
	}

	// Should have 2 cups: original EMG + auto-added NC
	if len(g.Cups) != 2 {
		t.Fatalf("expected 2 cups (EMG + NC), got %d", len(g.Cups))
	}

	nc := findCup(g.Cups, "891509")
	if nc == nil {
		t.Fatal("NC code 891509 should have been auto-added")
	}
	if nc.Quantity != 8 {
		t.Errorf("expected NC quantity=8 (2 EMG * 4), got %d", nc.Quantity)
	}
	if nc.Name != "NEUROCONDUCCION (CADA NERVIO)" {
		t.Errorf("expected NC name 'NEUROCONDUCCION (CADA NERVIO)', got %q", nc.Name)
	}

	// Espacios: 2 EMG <= 3 -> 1
	if g.Espacios != 1 {
		t.Errorf("expected Espacios=1 (2 EMG <= 3), got %d", g.Espacios)
	}
}

// 9. EMG + NC already present: NC quantity adjusted to totalEMG * 4.
func TestFisiatria_EMGWithNC(t *testing.T) {
	mock := newMock(
		struct {
			code, name, service string
			spaces              int
		}{"29120", "ELECTROMIOGRAFIA", "Fisiatria", 1},
		struct {
			code, name, service string
			spaces              int
		}{"891509", "NEUROCONDUCCION", "Fisiatria", 1},
	)

	cups := []CUPSEntry{
		cup("29120", "EMG", 1),
		cup("891509", "NC", 2), // original Qty=2 should be adjusted to 1*4=4
	}
	groups, err := GroupByServiceFromDB(context.Background(), cups, mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g := findGroup(groups, "Fisiatria")
	if g == nil {
		t.Fatal("missing 'Fisiatria' group")
	}

	nc := findCup(g.Cups, "891509")
	if nc == nil {
		t.Fatal("NC code 891509 should be present")
	}
	if nc.Quantity != 4 {
		t.Errorf("expected NC quantity=4 (1 EMG * 4), got %d", nc.Quantity)
	}

	// 1 EMG <= 3 -> Espacios=1
	if g.Espacios != 1 {
		t.Errorf("expected Espacios=1, got %d", g.Espacios)
	}
}

// 10. NC code without EMG: NC is removed, empty group gets Espacios=1.
func TestFisiatria_NCWithoutEMG(t *testing.T) {
	mock := newMock(struct {
		code, name, service string
		spaces              int
	}{"891509", "NEUROCONDUCCION", "Fisiatria", 1})

	cups := []CUPSEntry{cup("891509", "NC", 4)}
	groups, err := GroupByServiceFromDB(context.Background(), cups, mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g := findGroup(groups, "Fisiatria")
	if g == nil {
		t.Fatal("missing 'Fisiatria' group")
	}

	// NC should be removed (no EMG present)
	if len(g.Cups) != 0 {
		t.Errorf("expected 0 cups (NC removed without EMG), got %d", len(g.Cups))
	}
	// Minimum Espacios = 1
	if g.Espacios != 1 {
		t.Errorf("expected Espacios=1 (minimum), got %d", g.Espacios)
	}
}

// 11. EMG quantity >= 4 results in Espacios=2.
func TestFisiatria_EMGOver3(t *testing.T) {
	mock := newMock(struct {
		code, name, service string
		spaces              int
	}{"29120", "ELECTROMIOGRAFIA", "Fisiatria", 1})

	cups := []CUPSEntry{cup("29120", "EMG", 4)}
	groups, err := GroupByServiceFromDB(context.Background(), cups, mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g := findGroup(groups, "Fisiatria")
	if g == nil {
		t.Fatal("missing 'Fisiatria' group")
	}

	// 4 EMG >= 4 -> Espacios=2
	if g.Espacios != 2 {
		t.Errorf("expected Espacios=2 (4 EMG >= 4), got %d", g.Espacios)
	}

	// NC auto-added with quantity = 4 * 4 = 16
	nc := findCup(g.Cups, "891509")
	if nc == nil {
		t.Fatal("NC should have been auto-added")
	}
	if nc.Quantity != 16 {
		t.Errorf("expected NC quantity=16 (4 EMG * 4), got %d", nc.Quantity)
	}
}

// 12. EMG + dependent code (Onda F): both kept, NC auto-added.
func TestFisiatria_EMGWithDependent(t *testing.T) {
	mock := newMock(
		struct {
			code, name, service string
			spaces              int
		}{"29120", "ELECTROMIOGRAFIA", "Fisiatria", 1},
		struct {
			code, name, service string
			spaces              int
		}{"891514", "ONDA F", "Fisiatria", 1},
	)

	cups := []CUPSEntry{
		cup("29120", "EMG", 1),
		cup("891514", "Onda F", 1),
	}
	groups, err := GroupByServiceFromDB(context.Background(), cups, mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g := findGroup(groups, "Fisiatria")
	if g == nil {
		t.Fatal("missing 'Fisiatria' group")
	}

	// EMG should be present
	emg := findCup(g.Cups, "29120")
	if emg == nil {
		t.Error("EMG code should be kept")
	}

	// Dependent code should be kept (EMG is present)
	dep := findCup(g.Cups, "891514")
	if dep == nil {
		t.Error("dependent code 891514 should be kept when EMG is present")
	}

	// NC should be auto-added
	nc := findCup(g.Cups, "891509")
	if nc == nil {
		t.Fatal("NC should have been auto-added")
	}
	if nc.Quantity != 4 {
		t.Errorf("expected NC quantity=4 (1 EMG * 4), got %d", nc.Quantity)
	}

	// 3 cups total: EMG + dependent + NC
	if len(g.Cups) != 3 {
		t.Errorf("expected 3 cups (EMG + dependent + NC), got %d", len(g.Cups))
	}

	// 1 EMG <= 3 -> Espacios=1
	if g.Espacios != 1 {
		t.Errorf("expected Espacios=1, got %d", g.Espacios)
	}
}

// 13. Dependent code without EMG: dependent is removed.
func TestFisiatria_DependentWithoutEMG(t *testing.T) {
	mock := newMock(struct {
		code, name, service string
		spaces              int
	}{"891514", "ONDA F", "Fisiatria", 1})

	cups := []CUPSEntry{cup("891514", "Onda F", 1)}
	groups, err := GroupByServiceFromDB(context.Background(), cups, mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g := findGroup(groups, "Fisiatria")
	if g == nil {
		t.Fatal("missing 'Fisiatria' group")
	}

	// Dependent should be removed (no EMG present)
	if len(g.Cups) != 0 {
		t.Errorf("expected 0 cups (dependent removed without EMG), got %d", len(g.Cups))
	}
	if g.Espacios != 1 {
		t.Errorf("expected Espacios=1 (minimum), got %d", g.Espacios)
	}
}

// ===========================================================================
// Edge cases
// ===========================================================================

// 14. Mixed services: Fisiatria EMG rules only apply to the Fisiatria group.
func TestGroupByServiceFromDB_MixedServicesIsolation(t *testing.T) {
	mock := newMock(
		struct {
			code, name, service string
			spaces              int
		}{"29120", "ELECTROMIOGRAFIA", "Fisiatria", 1},
		struct {
			code, name, service string
			spaces              int
		}{"883101", "RM CEREBRAL", "Resonancia", 2},
	)

	cups := []CUPSEntry{
		cup("29120", "EMG", 2),
		cup("883101", "RM cerebral", 1),
	}
	groups, err := GroupByServiceFromDB(context.Background(), cups, mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}

	// Fisiatria group should have EMG rules applied
	fis := findGroup(groups, "Fisiatria")
	if fis == nil {
		t.Fatal("missing 'Fisiatria' group")
	}
	nc := findCup(fis.Cups, "891509")
	if nc == nil {
		t.Fatal("NC should be auto-added in Fisiatria group")
	}
	if fis.Espacios != 1 {
		t.Errorf("Fisiatria Espacios: expected 1, got %d", fis.Espacios)
	}

	// Resonancia group should NOT be affected by Fisiatria rules
	res := findGroup(groups, "Resonancia")
	if res == nil {
		t.Fatal("missing 'Resonancia' group")
	}
	if res.Espacios != 2 {
		t.Errorf("Resonancia Espacios: expected 2, got %d", res.Espacios)
	}
}

// 15. CUPS with empty code defaults to General.
func TestGroupByServiceFromDB_EmptyCode(t *testing.T) {
	mock := newMock()
	cups := []CUPSEntry{cup("", "Some procedure", 1)}
	groups, err := GroupByServiceFromDB(context.Background(), cups, mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if groups[0].ServiceType != "General" {
		t.Errorf("expected 'General', got %q", groups[0].ServiceType)
	}
	if groups[0].Espacios != 1 {
		t.Errorf("expected Espacios=1, got %d", groups[0].Espacios)
	}
}

// 16. Quantity=0 should still result in minimum Espacios=1.
func TestGroupByServiceFromDB_ZeroQuantity(t *testing.T) {
	mock := newMock(struct {
		code, name, service string
		spaces              int
	}{"883101", "RM CEREBRAL", "Resonancia", 2})

	cups := []CUPSEntry{cup("883101", "RM cerebral", 0)} // Quantity=0
	groups, err := GroupByServiceFromDB(context.Background(), cups, mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	// 0 qty * 2 spaces = 0, but minimum is 1
	if groups[0].Espacios != 1 {
		t.Errorf("expected minimum Espacios=1, got %d", groups[0].Espacios)
	}
}

// 17. Multiple NC codes with EMG: first NC adjusted, duplicates removed.
func TestFisiatria_MultipleNCWithEMG(t *testing.T) {
	mock := newMock(
		struct {
			code, name, service string
			spaces              int
		}{"29120", "ELECTROMIOGRAFIA", "Fisiatria", 1},
		struct {
			code, name, service string
			spaces              int
		}{"891509", "NEUROCONDUCCION", "Fisiatria", 1},
		struct {
			code, name, service string
			spaces              int
		}{"29103", "NC VARIANTE", "Fisiatria", 1},
	)

	cups := []CUPSEntry{
		cup("29120", "EMG", 2),
		cup("891509", "NC 1", 1),
		cup("29103", "NC 2", 1),
	}
	groups, err := GroupByServiceFromDB(context.Background(), cups, mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g := findGroup(groups, "Fisiatria")
	if g == nil {
		t.Fatal("missing 'Fisiatria' group")
	}

	// Duplicate NC codes should be removed; only first NC kept with adjusted qty
	ncCount := 0
	for _, c := range g.Cups {
		if ncCodes[c.Code] {
			ncCount++
			if c.Quantity != 8 {
				t.Errorf("NC quantity: expected 8 (2 EMG * 4), got %d", c.Quantity)
			}
		}
	}
	if ncCount != 1 {
		t.Errorf("expected exactly 1 NC code after dedup, got %d", ncCount)
	}

	// EMG + 1 NC = 2 cups
	if len(g.Cups) != 2 {
		t.Errorf("expected 2 cups (EMG + 1 NC), got %d", len(g.Cups))
	}
}

// 18. Fisiatria group with non-EMG/NC/dependent code plus NC only:
//     NC removed, non-EMG code stays.
func TestFisiatria_NonEMGCodePlusNC(t *testing.T) {
	mock := newMock(
		struct {
			code, name, service string
			spaces              int
		}{"890271", "CONSULTA FISIATRIA", "Fisiatria", 1},
		struct {
			code, name, service string
			spaces              int
		}{"891509", "NEUROCONDUCCION", "Fisiatria", 1},
	)

	cups := []CUPSEntry{
		cup("890271", "Consulta", 1),
		cup("891509", "NC", 4),
	}
	groups, err := GroupByServiceFromDB(context.Background(), cups, mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g := findGroup(groups, "Fisiatria")
	if g == nil {
		t.Fatal("missing 'Fisiatria' group")
	}

	// No EMG -> NC removed, non-EMG code kept
	if len(g.Cups) != 1 {
		t.Fatalf("expected 1 cup (NC removed), got %d", len(g.Cups))
	}
	if g.Cups[0].Code != "890271" {
		t.Errorf("expected remaining cup '890271', got %q", g.Cups[0].Code)
	}
	// Espacios = sum of non-NC quantities = 1
	if g.Espacios != 1 {
		t.Errorf("expected Espacios=1, got %d", g.Espacios)
	}
}

// 19. Group order is preserved based on insertion order.
func TestGroupByServiceFromDB_GroupOrder(t *testing.T) {
	mock := newMock(
		struct {
			code, name, service string
			spaces              int
		}{"890205", "CONSULTA NEUROLOGIA", "Neurologia", 1},
		struct {
			code, name, service string
			spaces              int
		}{"883101", "RM CEREBRAL", "Resonancia", 2},
		struct {
			code, name, service string
			spaces              int
		}{"890301", "CONSULTA NEUROCIRUGÍA", "Neurocirugia", 1},
	)

	cups := []CUPSEntry{
		cup("890205", "Consulta neuro", 1),
		cup("883101", "RM cerebral", 1),
		cup("890301", "Consulta NC", 1),
	}
	groups, err := GroupByServiceFromDB(context.Background(), cups, mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groups) != 3 {
		t.Fatalf("expected 3 groups, got %d", len(groups))
	}
	expected := []string{"Neurologia", "Resonancia", "Neurocirugia"}
	for i, exp := range expected {
		if groups[i].ServiceType != exp {
			t.Errorf("group[%d]: expected %q, got %q", i, exp, groups[i].ServiceType)
		}
	}
}

// 20. EMG boundary: exactly 3 EMG -> Espacios=1.
func TestFisiatria_EMGExactly3(t *testing.T) {
	mock := newMock(struct {
		code, name, service string
		spaces              int
	}{"29120", "ELECTROMIOGRAFIA", "Fisiatria", 1})

	cups := []CUPSEntry{cup("29120", "EMG", 3)}
	groups, err := GroupByServiceFromDB(context.Background(), cups, mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g := findGroup(groups, "Fisiatria")
	if g == nil {
		t.Fatal("missing 'Fisiatria' group")
	}
	if g.Espacios != 1 {
		t.Errorf("expected Espacios=1 (3 EMG <= 3), got %d", g.Espacios)
	}

	nc := findCup(g.Cups, "891509")
	if nc == nil {
		t.Fatal("NC should have been auto-added")
	}
	if nc.Quantity != 12 {
		t.Errorf("expected NC quantity=12 (3 EMG * 4), got %d", nc.Quantity)
	}
}
