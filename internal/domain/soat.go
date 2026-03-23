package domain

// SoatPrice represents a CUPS code with its pricing information
type SoatPrice struct {
	CupCode string
	Prices  map[string]float64 // tariff_01 -> price, tariff_02 -> price, etc.
}
