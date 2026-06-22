package main

type MasonNightData struct {
	Masons     []Player // other alive Masons, excluding self
	MasonCards []PlayerCardData
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
