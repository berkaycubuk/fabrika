package agent

import "github.com/berkaycubuk/fabrika/internal/model"

func Quarantined(recent []model.Attempt, threshold int) bool {
	if threshold <= 0 {
		return false
	}
	if len(recent) == 0 {
		return false
	}
	streak := 0
	for _, a := range recent {
		if a.Result != model.ResultFail {
			break
		}
		streak++
	}
	return streak >= threshold
}
