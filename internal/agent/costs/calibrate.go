package costs

import "math"

// Observation is one (tokens, cost) pair a tool recorded itself, for
// calibration; tokens are in millions: input, output, cache write, cache read.
type Observation struct {
	Model  string
	Tokens [4]float64
	Cost   float64
}

// calibrated solves rates from each tool's own (tokens, cost) pairs: at least
// four observations of a model, a non-negative solution and a residual under
// five percent of the observed cost. A field whose token column carries under
// 0.5% of the mass cannot be solved and keeps the built-in value. The records
// hold one cache-write count, so the 1h rate is derived as 1.6x the solved
// write rate (2.0 / 1.25).
func calibrated(home string) RateCard {
	out := RateCard{}
	for _, tool := range Tools {
		if tool.Source == nil {
			continue
		}
		byModel := map[string][]Observation{}
		for _, o := range tool.Source.Observations(home) {
			byModel[o.Model] = append(byModel[o.Model], o)
		}
		builtin := tool.Source.Builtin()
		for model, observed := range byModel {
			if rate, ok := calibrate(model, observed, builtin, tool.Tiers); ok {
				out[model] = rate
			}
		}
	}
	return out
}

func calibrate(model string, observed []Observation, builtin RateCard, tiers bool) (Rate, bool) {
	if len(observed) < 4 {
		return Rate{}, false
	}
	rows := make([][4]float64, len(observed))
	costs := make([]float64, len(observed))
	totalCost := 0.0
	for i, o := range observed {
		rows[i], costs[i] = o.Tokens, o.Cost
		totalCost += o.Cost
	}
	solution, ok := solveNormalEquations(rows, costs)
	if !ok {
		return Rate{}, false
	}
	for _, v := range solution {
		if v < 0 {
			return Rate{}, false
		}
	}
	residual := 0.0
	for i, row := range rows {
		predicted := 0.0
		for j := range row {
			predicted += row[j] * solution[j]
		}
		residual += math.Abs(predicted - costs[i])
	}
	if residual > 0.05*totalCost {
		return Rate{}, false
	}
	var mass [4]float64
	sum := 0.0
	for _, row := range rows {
		for j, v := range row {
			mass[j] += v
			sum += v
		}
	}
	if sum == 0 {
		sum = 1
	}
	family, haveFamily := builtin.Lookup(model, tiers)
	var rate Rate
	for j, field := range []string{"input", "output", "cache_write_5m", "cache_read"} {
		switch {
		case mass[j]/sum >= 0.005:
			rate.set(field, solution[j])
		case haveFamily:
			rate.set(field, family.field(field))
		default:
			return Rate{}, false
		}
	}
	rate.CacheWrite1h = rate.CacheWrite5m * 1.6
	rate.Source = "calibrated"
	return rate, true
}

// solveNormalEquations is least squares for four rates via AᵀA x = Aᵀb,
// Gauss-Jordan with partial pivoting. It reports false when the system is
// singular.
func solveNormalEquations(rows [][4]float64, costs []float64) ([4]float64, bool) {
	const n = 4
	var m [n][n + 1]float64
	for i := range n {
		for j := range n {
			for _, row := range rows {
				m[i][j] += row[i] * row[j]
			}
		}
		for k, row := range rows {
			m[i][n] += row[i] * costs[k]
		}
	}
	for c := range n {
		pivot := c
		for r := c; r < n; r++ {
			if math.Abs(m[r][c]) > math.Abs(m[pivot][c]) {
				pivot = r
			}
		}
		if math.Abs(m[pivot][c]) < 1e-12 {
			return [4]float64{}, false
		}
		m[c], m[pivot] = m[pivot], m[c]
		for r := range n {
			if r == c {
				continue
			}
			f := m[r][c] / m[c][c]
			for k := range m[r] {
				m[r][k] -= f * m[c][k]
			}
		}
	}
	var solution [4]float64
	for i := range n {
		solution[i] = m[i][n] / m[i][i]
	}
	return solution, true
}
