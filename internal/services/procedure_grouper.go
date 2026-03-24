package services

import (
	"context"

	"github.com/neuro-bot/neuro-bot/internal/repository"
)

// ── Resonancia Magnética (883xxx) rules ──────────────────────────────────────
// Slots required per CUPS code depending on whether the exam is contrasted.
// Source: config/ai.php from legacy Laravel system.
type resonanciaSlots struct{ Simple, Contrasted int }

var resonanciaCodes = map[string]resonanciaSlots{
	"883101": {1, 2}, // cerebro
	"883102": {1, 2}, // base de cráneo / silla turca
	"883103": {1, 2}, // órbitas
	"883104": {1, 2}, // cerebro funcional
	"883105": {1, 2}, // articulación temporomandibular
	"883106": {1, 2}, // tractografía (cerebro)
	"883107": {1, 2}, // dinámica de LCR
	"883108": {1, 2}, // pares craneanos
	"883109": {1, 2}, // oídos
	"883110": {1, 2}, // senos paranasales / cara
	"883111": {1, 2}, // cuello
	"883112": {1, 2}, // hipocampo volumétrico
	"883141": {1, 2}, // cerebro simple (variant)
	"883210": {1, 2}, // columna cervical
	"883211": {2, 2}, // columna cervical con contraste
	"883220": {1, 2}, // columna torácica
	"883221": {2, 2}, // columna torácica con contraste
	"883230": {1, 2}, // columna lumbosacra
	"883231": {2, 2}, // columna lumbar con contraste
	"883232": {1, 2}, // sacroilíaca
	"883233": {2, 2}, // sacroilíaca con contraste
	"883234": {1, 2}, // sacrococcígea
	"883235": {2, 2}, // sacrococcígea con contraste
	"883301": {1, 2}, // tórax
	"883321": {1, 2}, // corazón (morfología)
	"883341": {1, 2}, // angiorresonancia de tórax
	"883351": {2, 3}, // mama
	"883401": {1, 2}, // abdomen
	"883430": {1, 2}, // vías biliares
	"883434": {2, 3}, // colangioresonancia
	"883435": {1, 2}, // urorresonancia
	"883436": {1, 2}, // enterorresonancia
	"883440": {2, 2}, // pelvis
	"883441": {2, 3}, // dinámica de piso pélvico
	"883442": {1, 2}, // obstétrica
	"883443": {1, 2}, // placenta
	"883511": {1, 2}, // miembro superior (sin articulaciones)
	"883512": {1, 2}, // articulaciones miembro superior
	"883521": {1, 2}, // miembro inferior (sin articulaciones)
	"883522": {1, 2}, // articulaciones miembro inferior
	"883560": {1, 2}, // plexo braquial
	"883590": {1, 2}, // sistema músculo-esquelético
	"883902": {1, 2}, // RM con perfusión
	"883904": {1, 2}, // RM de sitio no especificado
	"883909": {1, 2}, // RM con angiografía
	"883913": {1, 2}, // difusión
}

// Combination rules: when all listed codes appear together, use combined slot count
// instead of summing individual slots. Source: legacy Laravel ai.php config.
type resonanciaCombination struct {
	codes      []string
	simple     int
	contrasted int
}

var resonanciaCombinations = []resonanciaCombination{
	{codes: []string{"883401", "883440"}, simple: 2, contrasted: 3}, // abdomen + pelvis
	{codes: []string{"883902", "883904"}, simple: 1, contrasted: 2}, // perfusión combination
}

// sedacionResonanciaCode adds extra slots when combined with resonancia.
const sedacionResonanciaCode = "998702"

// ── Fisiatría EMG code groups (from institutional rules) ─────────────────────
var emgCodes = map[string]bool{
	"29120": true, "930810": true, "892302": true, "892301": true,
	"930820": true, "930860": true, "893601": true, "930801": true,
	"29101": true, "000005": true, "000006": true, "000004": true,
}

var ncCodes = map[string]bool{
	"29103":  true, "891509": true, "29102": true,
	"891098": true, // NEUROCONDUCCION POR CADA EXTREMIDAD
}

var emgDependentCodes = map[string]bool{
	"891514": true, "891515": true,
	"891503": true, // REFLEJO NEUROLOGICO TRIGEMINO FACIAL
}

type enrichedCup struct {
	CUPSEntry
	ServiceName    string
	RequiredSpaces int
}

// GroupByServiceFromDB groups CUPS entries by service using DB data (deterministic, no AI).
// Each CUPS is looked up in the DB to get ServiceName and RequiredSpaces.
// Special rules apply for Fisiatría (EMG/NC grouping).
func GroupByServiceFromDB(ctx context.Context, cups []CUPSEntry, procRepo repository.ProcedureRepository) ([]CUPSGroup, error) {
	if len(cups) == 0 {
		return []CUPSGroup{{ServiceType: "General", Cups: cups, Espacios: 1}}, nil
	}

	// Enrich each CUPS with DB data; skip inactive/unknown codes
	enriched := make([]enrichedCup, 0, len(cups))
	for _, c := range cups {
		ec := enrichedCup{CUPSEntry: c, ServiceName: "General", RequiredSpaces: 1}
		if c.Code != "" {
			proc, err := procRepo.FindByCode(ctx, c.Code)
			if err != nil || proc == nil {
				// Code not found or inactive (activo=0) — skip it
				continue
			}
			ec.Name = proc.Name
			// Force EMG/NC codes to Fisiatria only if active in DB
			if emgCodes[c.Code] || ncCodes[c.Code] || emgDependentCodes[c.Code] {
				ec.ServiceName = "Fisiatria"
			} else if proc.ServiceName != "" {
				ec.ServiceName = proc.ServiceName
			}
			if proc.RequiredSpaces >= 1 {
				ec.RequiredSpaces = proc.RequiredSpaces
			}
		}
		enriched = append(enriched, ec)
	}

	// Override: force all resonancia codes to "Resonancia" service
	for i, ec := range enriched {
		if _, isRM := resonanciaCodes[ec.Code]; isRM {
			enriched[i].ServiceName = "Resonancia"
		}
		if ec.Code == sedacionResonanciaCode {
			enriched[i].ServiceName = "Resonancia"
		}
	}

	// Group by service name (maintain insertion order)
	groupOrder := []string{}
	groupMap := map[string][]enrichedCup{}
	for _, ec := range enriched {
		if _, exists := groupMap[ec.ServiceName]; !exists {
			groupOrder = append(groupOrder, ec.ServiceName)
		}
		groupMap[ec.ServiceName] = append(groupMap[ec.ServiceName], ec)
	}

	// Build result groups
	groups := make([]CUPSGroup, 0, len(groupOrder))
	for _, svc := range groupOrder {
		ecs := groupMap[svc]
		cupEntries := make([]CUPSEntry, len(ecs))
		totalEspacios := 0
		for i, ec := range ecs {
			cupEntries[i] = ec.CUPSEntry
			totalEspacios += ec.RequiredSpaces * ec.Quantity
		}
		if totalEspacios < 1 {
			totalEspacios = 1
		}
		groups = append(groups, CUPSGroup{
			ServiceType: svc,
			Cups:        cupEntries,
			Espacios:    totalEspacios,
		})
	}

	// Apply Fisiatría special rules
	for i, g := range groups {
		if g.ServiceType == "Fisiatria" {
			groups[i] = applyFisiatriaRules(g)
		}
	}

	// Apply Resonancia special rules
	for i, g := range groups {
		if g.ServiceType == "Resonancia" {
			groups[i] = applyResonanciaRules(g)
		}
	}

	return groups, nil
}

// applyResonanciaRules calculates the correct number of consecutive slots for an
// MRI group based on:
//  1. Whether each exam is contrasted (IsContrasted on the CUPSEntry)
//  2. Combination rules (e.g. abdomen+pelvis together = 3 slots contrasted, not 4)
//  3. Sedation code 998702 (adds +1 simple / +2 contrasted, never alone)
func applyResonanciaRules(g CUPSGroup) CUPSGroup {
	// Determine if any RM in the group is contrasted
	isContrasted := false
	for _, c := range g.Cups {
		if c.IsContrasted {
			isContrasted = true
			break
		}
	}

	// Collect present RM codes (excluding sedation)
	codeSet := make(map[string]bool)
	for _, c := range g.Cups {
		if c.Code != sedacionResonanciaCode {
			codeSet[c.Code] = true
		}
	}

	// Check if any combination rule applies (all codes in rule must be present)
	combinationSpaces := -1
	for _, combo := range resonanciaCombinations {
		allPresent := true
		for _, code := range combo.codes {
			if !codeSet[code] {
				allPresent = false
				break
			}
		}
		if allPresent {
			if isContrasted {
				combinationSpaces = combo.contrasted
			} else {
				combinationSpaces = combo.simple
			}
			break // use first matching combination
		}
	}

	totalSpaces := 0
	if combinationSpaces >= 0 {
		// Use combined slot count; add individual slots for any codes NOT in the combination
		// (find which combination matched and which codes are outside it)
		totalSpaces = combinationSpaces
		// Add slots for RM codes not covered by the combination
		for _, c := range g.Cups {
			if c.Code == sedacionResonanciaCode {
				continue
			}
			inCombo := false
			for _, combo := range resonanciaCombinations {
				if combinationSpaces >= 0 {
					for _, cc := range combo.codes {
						if cc == c.Code {
							inCombo = true
							break
						}
					}
				}
			}
			if !inCombo {
				if slots, ok := resonanciaCodes[c.Code]; ok {
					if isContrasted {
						totalSpaces += slots.Contrasted * c.Quantity
					} else {
						totalSpaces += slots.Simple * c.Quantity
					}
				} else {
					totalSpaces += c.Quantity
				}
			}
		}
	} else {
		// No combination: sum individual slots per code
		for _, c := range g.Cups {
			if c.Code == sedacionResonanciaCode {
				continue
			}
			if slots, ok := resonanciaCodes[c.Code]; ok {
				if isContrasted {
					totalSpaces += slots.Contrasted * c.Quantity
				} else {
					totalSpaces += slots.Simple * c.Quantity
				}
			} else {
				totalSpaces += c.Quantity
			}
		}
	}

	// Add sedation slots: +1 simple, +2 contrasted (only if there are other RM procedures)
	hasSedacion := false
	for _, c := range g.Cups {
		if c.Code == sedacionResonanciaCode {
			hasSedacion = true
			break
		}
	}
	if hasSedacion && totalSpaces > 0 {
		if isContrasted {
			totalSpaces += 2
		} else {
			totalSpaces += 1
		}
	}

	if totalSpaces < 1 {
		totalSpaces = 1
	}
	g.Espacios = totalSpaces
	return g
}

// applyFisiatriaRules implements institutional EMG/NC grouping rules:
// - NC quantity = total EMG quantity × 4
// - If no NC in order, add 891509
// - If NC/dependent codes exist without EMG, remove them
// - Espacios: ≤3 EMG → 1, ≥4 → 2
func applyFisiatriaRules(g CUPSGroup) CUPSGroup {
	totalEMG := 0
	hasNC := false
	ncIdx := -1

	for i, c := range g.Cups {
		if emgCodes[c.Code] {
			totalEMG += c.Quantity
		}
		if ncCodes[c.Code] {
			hasNC = true
			if ncIdx == -1 {
				ncIdx = i
			}
		}
	}

	// No EMG procedures → remove NC/dependent codes, keep the rest
	if totalEMG == 0 {
		valid := make([]CUPSEntry, 0, len(g.Cups))
		espacios := 0
		for _, c := range g.Cups {
			if !ncCodes[c.Code] && !emgDependentCodes[c.Code] {
				valid = append(valid, c)
				espacios += c.Quantity
			}
		}
		if espacios < 1 {
			espacios = 1
		}
		g.Cups = valid
		g.Espacios = espacios
		return g
	}

	ncQuantity := totalEMG * 4

	if !hasNC {
		// Add NC procedure
		g.Cups = append(g.Cups, CUPSEntry{
			Code:     "891509",
			Name:     "NEUROCONDUCCION (CADA NERVIO)",
			Quantity: ncQuantity,
		})
	} else {
		// Adjust first NC quantity, remove duplicates
		adjusted := false
		filtered := make([]CUPSEntry, 0, len(g.Cups))
		for _, c := range g.Cups {
			if ncCodes[c.Code] {
				if !adjusted {
					c.Quantity = ncQuantity
					adjusted = true
					filtered = append(filtered, c)
				}
				// Skip duplicate NC codes
			} else {
				filtered = append(filtered, c)
			}
		}
		g.Cups = filtered
	}

	// Espacios based on total EMG count
	if totalEMG <= 3 {
		g.Espacios = 1
	} else {
		g.Espacios = 2
	}

	return g
}
