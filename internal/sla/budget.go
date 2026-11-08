package sla

import (
	"math"
	"math/big"
	"strconv"
)

// decimalPercent treats a configured percentage as the decimal value users
// entered. Binary subtraction (100-99.9) otherwise loses a whole displayed
// second when an exact 2,592-second allowance is truncated to 2,591.
func decimalPercent(value float64) *big.Rat {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return new(big.Rat)
	}
	result, _ := new(big.Rat).SetString(strconv.FormatFloat(value, 'f', -1, 64))
	return result
}

func allowance(target float64, seconds int64) *big.Rat {
	if target <= 0 || target >= 100 || seconds <= 0 || math.IsNaN(target) {
		return new(big.Rat)
	}
	remaining := new(big.Rat).Sub(big.NewRat(100, 1), decimalPercent(target))
	return remaining.Mul(remaining, big.NewRat(seconds, 100))
}

func wholeSeconds(value *big.Rat) int64 {
	return new(big.Int).Quo(value.Num(), value.Denom()).Int64()
}

type budgetValues struct {
	Total, Used, Remain int64
	RemainingPercent    float64
	Breached            bool
	AtRisk              bool
}

// monthlyBudget compares exact sample counts with the decimal monthly target.
// Rounding is confined to whole-second presentation; it never decides a breach.
func monthlyBudget(target float64, totalChecks, upChecks, totalSecs, elapsedSecs int64) budgetValues {
	allowed := allowance(target, totalSecs)
	used := new(big.Rat)
	if totalChecks > 0 && elapsedSecs > 0 {
		used.Mul(big.NewRat(totalChecks-min(max(upChecks, 0), totalChecks), totalChecks), big.NewRat(elapsedSecs, 1))
	}
	values := budgetValues{Total: wholeSeconds(allowed), Used: wholeSeconds(used), RemainingPercent: 100}
	remaining := new(big.Rat).Sub(allowed, used)
	if remaining.Sign() > 0 {
		values.Remain = wholeSeconds(remaining)
	}
	values.Breached = target > 0 && used.Cmp(allowed) > 0
	values.AtRisk = allowed.Sign() > 0 && !values.Breached && new(big.Rat).Mul(remaining, big.NewRat(10, 1)).Cmp(allowed) <= 0
	if allowed.Sign() > 0 {
		remaining.Quo(remaining, allowed).Mul(remaining, big.NewRat(100, 1))
		values.RemainingPercent, _ = remaining.Float64()
		values.RemainingPercent = max(values.RemainingPercent, 0)
	} else if used.Sign() > 0 {
		values.RemainingPercent = 0
	}
	return values
}

// ErrorBudgetSecs returns whole seconds of allowed downtime for the month.
func ErrorBudgetSecs(target float64, totalSecs int64) int64 {
	return wholeSeconds(allowance(target, totalSecs))
}

// DowntimeSecs estimates whole seconds from a decimal uptime percentage.
func DowntimeSecs(uptimePct float64, elapsedSecs int64) int64 {
	if elapsedSecs <= 0 || uptimePct >= 100 || math.IsNaN(uptimePct) {
		return 0
	}
	if uptimePct <= 0 {
		return elapsedSecs
	}
	return wholeSeconds(allowance(uptimePct, elapsedSecs))
}
