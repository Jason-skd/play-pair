package api

import (
	"net/http"

	"github.com/Jason-skd/play-pair/internal/api/generated"
	"github.com/Jason-skd/play-pair/internal/preference"
	"github.com/gin-gonic/gin"
)

func (s *server) GetPreferences(c *gin.Context) {
	id, ok := s.resourceActor(c)
	if !ok {
		return
	}
	p, err := s.services.Preferences.Get(c.Request.Context(), id)
	if err != nil {
		s.resourceFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, preferenceResponse(p))
}
func (s *server) PutPreferences(c *gin.Context, params generated.PutPreferencesParams) {
	id, ok := s.resourceActor(c)
	if !ok {
		return
	}
	if string(params.XPlayPairRequest) != "1" {
		writeError(c, 400, generated.CsrfRejected, "Invalid browser request")
		return
	}
	var body generated.UpdatePreference
	if !decodeReplacement(c, &body,
		[]string{"soft", "soft.likes", "soft.neutral", "soft.dislikes", "hardDefaults", "hardDefaults.maxBudgetFen", "hardDefaults.requiresWheelchairAccess", "expectedVersion"},
		[]string{"hardDefaults.maxBudgetFen", "hardDefaults.requiresWheelchairAccess"}) {
		return
	}
	p, err := s.services.Preferences.Replace(c.Request.Context(), id, preference.Update{
		Soft:         preference.Soft{Likes: body.Soft.Likes, Neutral: body.Soft.Neutral, Dislikes: body.Soft.Dislikes},
		HardDefaults: preference.HardDefaults{MaxBudgetFen: body.HardDefaults.MaxBudgetFen, RequiresWheelchairAccess: body.HardDefaults.RequiresWheelchairAccess}, ExpectedVersion: body.ExpectedVersion,
	})
	if err != nil {
		s.resourceFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, preferenceResponse(p))
}
func preferenceResponse(p preference.Preference) generated.Preference {
	return generated.Preference{Soft: generated.SoftPreferences{Likes: p.Soft.Likes, Neutral: p.Soft.Neutral, Dislikes: p.Soft.Dislikes}, HardDefaults: generated.HardDefaults{MaxBudgetFen: p.HardDefaults.MaxBudgetFen, RequiresWheelchairAccess: p.HardDefaults.RequiresWheelchairAccess}, Version: p.Version}
}
