package main

// MasonNightData holds night-phase display data for Masons.
type MasonNightData struct {
	Masons     []Player         // other alive Masons (excluding self); full role visible
	MasonCards []PlayerCardData // cards for the fellow masons
}

func buildMasonNightData(player Player, players []Player) MasonNightData {
	if player.RoleName != "Mason" {
		return MasonNightData{}
	}

	d := MasonNightData{}
	for _, p := range players {
		if p.RoleName == "Mason" && p.IsAlive && p.PlayerID != player.PlayerID {
			d.Masons = append(d.Masons, p)
		}
	}
	return d
}
