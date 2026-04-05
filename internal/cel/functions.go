package cel

// SpotRiskFunc returns the spot interruption probability for a given instance type and AZ.
// For MVP: heuristic values based on instance family; replaced by ML model in Phase 6.
func SpotRiskFunc(instanceType, _ string) float64 {
	riskMap := map[string]float64{
		"m5.large":   0.15,
		"m5.xlarge":  0.12,
		"m5.2xlarge": 0.08,
		"c5.large":   0.18,
		"c5.xlarge":  0.14,
		"r5.large":   0.10,
	}
	if risk, ok := riskMap[instanceType]; ok {
		return risk
	}
	return 0.20 // default: 20% interruption risk
}

// CarbonIntensityFunc returns gCO2/kWh for a cloud region.
// Source: electricityMap regional averages; replaced by live API in a future release.
func CarbonIntensityFunc(region string) float64 {
	carbonMap := map[string]float64{
		"us-east-1":      380,
		"us-west-2":      120, // hydro-heavy
		"eu-west-1":      300,
		"eu-north-1":     50, // Nordic, very clean
		"ap-southeast-1": 500,
		"us-central1":    450, // GCP
	}
	if c, ok := carbonMap[region]; ok {
		return c
	}
	return 400 // global average
}

// CostRateFunc returns hourly cost in USD for an instance type.
// Simplified pricing; replaced by a live pricing API in a future release.
func CostRateFunc(instanceType, _ string, spot bool) float64 {
	basePrices := map[string]float64{
		"m5.large":   0.096,
		"m5.xlarge":  0.192,
		"m5.2xlarge": 0.384,
		"c5.large":   0.085,
		"c5.xlarge":  0.170,
		"r5.large":   0.126,
	}
	price := basePrices[instanceType]
	if price == 0 {
		price = 0.10 // default
	}
	if spot {
		price *= 0.3 // ~70% spot discount
	}
	return price
}
