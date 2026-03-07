package services

import (
	"context"

	"github.com/neuro-bot/neuro-bot/internal/repository"
)

// Fisiatría EMG code groups (from institutional rules)
var emgCodes = map[string]bool{
	"29120": true, "930810": true, "892302": true, "892301": true,
	"930820": true, "930860": true, "893601": true, "930801": true,
	"29101": true, "000005": true, "000006": true, "000004": true,
}

var ncCodes = map[string]bool{
	"29103": true, "891509": true, "29102": true,
}

var emgDependentCodes = map[string]bool{
	"891514": true, "891515": true,
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

	// Enrich each CUPS with DB data
	enriched := make([]enrichedCup, 0, len(cups))
	for _, c := range cups {
		ec := enrichedCup{CUPSEntry: c, ServiceName: "General", RequiredSpaces: 1}
		if c.Code != "" {
			proc, err := procRepo.FindByCode(ctx, c.Code)
			if err == nil && proc != nil {
				ec.Name = proc.Name // Enrich name from DB
				if proc.ServiceName != "" {
					ec.ServiceName = proc.ServiceName
				}
				if proc.RequiredSpaces >= 1 {
					ec.RequiredSpaces = proc.RequiredSpaces
				}
			}
		}
		enriched = append(enriched, ec)
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

	return groups, nil
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
