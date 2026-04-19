package main

import (
	"fmt"
	"net/http"
)

var translations = map[string]map[string]string{
	"en": {
		"lang_name": "English",

		// Index page
		"page_title_index":         "Werewolf - Sign In",
		"join_game_heading":        "Join Game",
		"game_name_label":          "Game Name",
		"game_name_placeholder":    "Enter game name",
		"btn_join":                 "Join Game",
		"btn_logout":               "Logout",
		"new_player_heading":       "New Player",
		"choose_name_label":        "Choose your name",
		"name_placeholder":         "Enter your name",
		"returning_player_heading": "Returning Player",
		"returning_player_desc":    "Already signed up on another device? Enter your name and secret code.",
		"name_label":               "Name",
		"your_name_placeholder":    "Your name",
		"secret_code_label":        "Secret Code",
		"secret_code_placeholder":  "Your secret code",
		"btn_login":                "Login",

		// Sidebar
		"sidebar_players":  "Players",
		"narrator_unmuted": "🔊 Narrator",
		"narrator_muted":   "🔇 Narrator",
		"code_label":       "Code",
		"night_round":      "Night %d",
		"day_round":        "Day %d",

		// Lobby
		"players_label":     "Players:",
		"roles_label":       "Roles:",
		"ready_to_start":    "Ready to start!",
		"need_more_players": "Need %d more players",
		"need_more_roles":   "Need %d more roles",
		"configure_roles":   "Configure roles below",
		"roles_heading":     "Roles",
		"roles_desc":        "Select which roles and how many of each to include in the game.",
		"btn_start_game":    "Start Game",

		// Night general
		"waiting_for_players": "Waiting for %d more player(s)...",
		"you_are_dead_night":  "You are dead. The village sleeps around you.",
		"village_sleeps":      "The village sleeps...",
		"close_eyes":          "Close your eyes and wait for morning.",
		"storyteller_asking":  "The storyteller is asking you",
		"who_is_werewolf":     "Who do you think is a Werewolf?",
		"how_victim_died":     "How do you think the victim died?",
		"optional":            "(optional)",
		"notes_label":         "Notes",
		"btn_continue":        "Continue →",

		// Night: Werewolf
		"werewolf_title":       "Werewolf: Choose a Victim",
		"vote_locked_waiting":  "Vote locked in. Waiting for night to end...",
		"werewolf_select_desc": "Select a player to kill, or pass. When all werewolves have acted, end the vote.",
		"btn_pass":             "Pass",
		"btn_end_vote":         "End Vote",
		"current_votes":        "Current Votes",
		"vote_pass":            "Pass",
		"wolf_cub_title":       "Wolf Cub's Revenge — Second Victim",
		"vote2_locked":         "Second vote locked in. Waiting for night to end...",
		"wolf_cub_desc":        "The Wolf Cub was slain. Choose a second player to kill tonight, or pass.",
		"btn_end_second_vote":  "End Second Vote",

		// Night: Seer
		"seer_title":        "Seer: Your Investigation",
		"seer_already_done": "You have already investigated tonight.",
		"seer_choose":       "Choose a player to investigate, then confirm your choice.",
		"btn_investigate":   "🔮 Investigate",

		// Night: Doctor
		"doctor_title":       "Doctor: Your Protection",
		"doctor_protecting":  "You are protecting %s tonight.",
		"doctor_choose":      "Choose a player to protect, then confirm.",
		"btn_doctor_protect": "🩺 Protect",

		// Night: Guard
		"guard_title":       "Guard: Your Protection",
		"guard_protecting":  "You are protecting %s tonight.",
		"guard_choose":      "Choose a player to protect, then confirm. You cannot protect yourself or the same player twice in a row.",
		"btn_guard_protect": "🛡️ Protect",

		// Night: Witch
		"witch_title":         "Witch: Your Potions",
		"witch_saved":         "✓ You saved %s with your heal potion.",
		"witch_poisoned":      "☠️ You poisoned %s.",
		"witch_waiting":       "Waiting for the night to end...",
		"heal_potion":         "🧪 Heal Potion",
		"witch_targeting":     "The werewolves are targeting (click to save, click again to deselect):",
		"witch_no_target":     "The werewolves have not chosen a target yet...",
		"heal_potion_used":    "Your heal potion has been used.",
		"poison_potion":       "☠️ Poison Potion",
		"witch_poison_choose": "Choose a player to poison (click to select, click again to deselect):",
		"poison_potion_used":  "Your poison potion has been used.",
		"btn_witch_done":      "✓ Done for tonight",

		// Night: Mason
		"mason_title":      "Mason: Your Fellow Masons",
		"mason_know_these": "You know these confirmed villagers:",
		"mason_alone":      "You are the only Mason.",

		// Night: Cupid
		"cupid_title":      "Cupid: Link Two Lovers",
		"cupid_linked":     "You have linked %s and %s as lovers.",
		"cupid_chosen_two": "Chosen: %s and %s. Click a card to deselect.",
		"cupid_one_chosen": "One chosen. Select the second lover.",
		"cupid_choose_two": "Choose two players to become lovers. They will know each other's identities.",
		"btn_cupid_link":   "💞 Link lovers",

		// Night: Doppelganger
		"doppelganger_title":      "Doppelganger: Choose Your Identity",
		"doppelganger_became":     "You have become %s (copied from %s).",
		"doppelganger_selected":   "Selected: %s. Click to deselect or confirm below.",
		"doppelganger_choose":     "Choose a player. You will secretly become their role for the rest of the game.",
		"btn_doppelganger_become": "🎭 Become",

		// Day phase
		"no_deaths_last_night":   "The village awakens. No one died last night.",
		"hunter_shot_killed":     "🏹 The Hunter's last shot killed %s!",
		"hunter_victim_was":      "They were a %s.",
		"hunter_last_shot":       "Your Last Shot",
		"hunter_eliminated_desc": "You have been eliminated! Choose a player to take down with you, then confirm.",
		"btn_hunter_shoot":       "🏹 Shoot",
		"hunter_choosing":        "The Hunter is choosing their final target...",
		"vote_to_eliminate":      "Vote to Eliminate",
		"choose_to_eliminate":    "Choose a player to eliminate, or pass. Majority vote required.",
		"dead_cannot_vote":       "You are dead and cannot vote.",
		"card_alive":             "Alive",
		"card_dead":              "Dead",

		// Role names and descriptions (for player cards)
		"role_name_Villager":     "Villager",
		"role_name_Werewolf":     "Werewolf",
		"role_name_Seer":         "Seer",
		"role_name_Doctor":       "Doctor",
		"role_name_Witch":        "Witch",
		"role_name_Hunter":       "Hunter",
		"role_name_Cupid":        "Cupid",
		"role_name_Guard":        "Guard",
		"role_name_Mason":        "Mason",
		"role_name_Wolf Cub":     "Wolf Cub",
		"role_name_Doppelganger": "Doppelganger",
		"role_desc_Villager":     "No special powers, relies on deduction and discussion.",
		"role_desc_Werewolf":     "Knows other werewolves, votes to kill villagers at night.",
		"role_desc_Seer":         "Can investigate one player per night to learn if they are a werewolf.",
		"role_desc_Doctor":       "Can protect one player from werewolf attack each night.",
		"role_desc_Witch":        "Has one heal potion and one poison potion to use during the game.",
		"role_desc_Hunter":       "When eliminated, can immediately kill one player.",
		"role_desc_Cupid":        "On night 1, chooses two players to become lovers.",
		"role_desc_Guard":        "Protects one player per night, but not the same player twice in a row.",
		"role_desc_Mason":        "Knows other masons, providing confirmed villagers.",
		"role_desc_Wolf Cub":     "If eliminated, werewolves kill two victims the next night.",
		"role_desc_Doppelganger": "On night 1, secretly copies another player's role and becomes that role for the rest of the game.",

		// Finished screen
		"victors":            "Victors",
		"the_fallen":         "The Fallen",
		"btn_play_again":     "Play Again",
		"villagers_win_alt":  "Villagers win",
		"lovers_win_alt":     "Lovers win",
		"werewolves_win_alt": "Werewolves win",

		// Error/toast messages
		"err_game_name_required":   "Game name is required",
		"err_name_required":        "Name is required",
		"err_name_taken":           "Name already taken. Use login with secret code if this is you.",
		"err_something_wrong":      "Something went wrong",
		"err_name_code_required":   "Name and secret code are required",
		"err_invalid_credentials":  "Invalid name or secret code",
		"err_failed_get_game":      "Failed to get game",
		"err_game_already_started": "Cannot update roles: game already started",
		"err_game_started":         "Game already started",
		"err_failed_get_players":   "Failed to get players",
		"err_failed_get_roles":     "Failed to get role configuration",
		"err_role_count_mismatch":  "Role count must match player count",
		"err_failed_assign_joker":  "Failed to assign Joker role",
		"err_failed_assign_roles":  "Failed to assign roles",
		"err_failed_start_game":    "Failed to start game",
		"err_game_not_finished":    "Game is not finished yet",
		"err_failed_role_config":   "Failed to get role config",
		"err_failed_create_game":   "Failed to create new game",
	},
	"de": {
		"lang_name": "Deutsch",

		// Index page
		"page_title_index":         "Werwolf - Anmelden",
		"join_game_heading":        "Spiel beitreten",
		"game_name_label":          "Spielname",
		"game_name_placeholder":    "Spielname eingeben",
		"btn_join":                 "Beitreten",
		"btn_logout":               "Abmelden",
		"new_player_heading":       "Neuer Spieler",
		"choose_name_label":        "Wähle deinen Namen",
		"name_placeholder":         "Namen eingeben",
		"returning_player_heading": "Zurückkehrender Spieler",
		"returning_player_desc":    "Bereits auf einem anderen Gerät angemeldet? Gib deinen Namen und Geheimcode ein.",
		"name_label":               "Name",
		"your_name_placeholder":    "Dein Name",
		"secret_code_label":        "Geheimcode",
		"secret_code_placeholder":  "Dein Geheimcode",
		"btn_login":                "Anmelden",

		// Sidebar
		"sidebar_players":  "Spieler",
		"narrator_unmuted": "🔊 Erzähler",
		"narrator_muted":   "🔇 Erzähler",
		"code_label":       "Code",
		"night_round":      "Nacht %d",
		"day_round":        "Tag %d",

		// Lobby
		"players_label":     "Spieler:",
		"roles_label":       "Rollen:",
		"ready_to_start":    "Bereit zum Starten!",
		"need_more_players": "%d weitere Spieler benötigt",
		"need_more_roles":   "%d weitere Rollen benötigt",
		"configure_roles":   "Rollen unten konfigurieren",
		"roles_heading":     "Rollen",
		"roles_desc":        "Wähle welche Rollen und wie viele davon im Spiel enthalten sein sollen.",
		"btn_start_game":    "Spiel starten",

		// Night general
		"waiting_for_players": "Warte auf %d weitere Spieler...",
		"you_are_dead_night":  "Du bist tot. Das Dorf schläft um dich herum.",
		"village_sleeps":      "Das Dorf schläft...",
		"close_eyes":          "Schließe die Augen und warte auf den Morgen.",
		"storyteller_asking":  "Der Erzähler fragt dich",
		"who_is_werewolf":     "Wer ist deiner Meinung nach ein Werwolf?",
		"how_victim_died":     "Wie glaubst du, ist das Opfer gestorben?",
		"optional":            "(optional)",
		"notes_label":         "Notizen",
		"btn_continue":        "Weiter →",

		// Night: Werewolf
		"werewolf_title":       "Werwolf: Wähle ein Opfer",
		"vote_locked_waiting":  "Abstimmung gesperrt. Warte auf das Ende der Nacht...",
		"werewolf_select_desc": "Wähle einen Spieler zum Töten, oder passe. Wenn alle Werwölfe gehandelt haben, beende die Abstimmung.",
		"btn_pass":             "Passen",
		"btn_end_vote":         "Abstimmung beenden",
		"current_votes":        "Aktuelle Abstimmungen",
		"vote_pass":            "Passen",
		"wolf_cub_title":       "Wolfswelpe Rache — Zweites Opfer",
		"vote2_locked":         "Zweite Abstimmung gesperrt. Warte auf das Ende der Nacht...",
		"wolf_cub_desc":        "Der Wolfswelpe wurde getötet. Wähle heute Nacht einen zweiten Spieler zum Töten, oder passe.",
		"btn_end_second_vote":  "Zweite Abstimmung beenden",

		// Night: Seer
		"seer_title":        "Seher: Deine Untersuchung",
		"seer_already_done": "Du hast heute Nacht bereits untersucht.",
		"seer_choose":       "Wähle einen Spieler zum Untersuchen und bestätige dann deine Wahl.",
		"btn_investigate":   "🔮 Untersuchen",

		// Night: Doctor
		"doctor_title":       "Arzt: Dein Schutz",
		"doctor_protecting":  "Du schützt heute Nacht %s.",
		"doctor_choose":      "Wähle einen Spieler zum Schützen und bestätige dann.",
		"btn_doctor_protect": "🩺 Schützen",

		// Night: Guard
		"guard_title":       "Wächter: Dein Schutz",
		"guard_protecting":  "Du schützt heute Nacht %s.",
		"guard_choose":      "Wähle einen Spieler zum Schützen und bestätige dann. Du kannst dich nicht selbst oder denselben Spieler zweimal hintereinander schützen.",
		"btn_guard_protect": "🛡️ Schützen",

		// Night: Witch
		"witch_title":         "Hexe: Deine Tränke",
		"witch_saved":         "✓ Du hast %s mit deinem Heiltrank gerettet.",
		"witch_poisoned":      "☠️ Du hast %s vergiftet.",
		"witch_waiting":       "Warte auf das Ende der Nacht...",
		"heal_potion":         "🧪 Heiltrank",
		"witch_targeting":     "Die Werwölfe zielen auf (klicken zum Retten, erneut klicken zum Abwählen):",
		"witch_no_target":     "Die Werwölfe haben noch kein Ziel gewählt...",
		"heal_potion_used":    "Dein Heiltrank wurde verwendet.",
		"poison_potion":       "☠️ Gifttrank",
		"witch_poison_choose": "Wähle einen Spieler zum Vergiften (klicken zum Auswählen, erneut klicken zum Abwählen):",
		"poison_potion_used":  "Dein Gifttrank wurde verwendet.",
		"btn_witch_done":      "✓ Für heute Nacht fertig",

		// Night: Mason
		"mason_title":      "Freimaurer: Deine Mitmaurer",
		"mason_know_these": "Du kennst diese bestätigten Dorfbewohner:",
		"mason_alone":      "Du bist der einzige Freimaurer.",

		// Night: Cupid
		"cupid_title":      "Amor: Verbinde zwei Liebende",
		"cupid_linked":     "Du hast %s und %s als Liebende verbunden.",
		"cupid_chosen_two": "Ausgewählt: %s und %s. Klicke auf eine Karte zum Abwählen.",
		"cupid_one_chosen": "Eine Person ausgewählt. Wähle den zweiten Liebenden.",
		"cupid_choose_two": "Wähle zwei Spieler als Liebende. Sie werden voneinander die Identität kennen.",
		"btn_cupid_link":   "💞 Liebende verbinden",

		// Night: Doppelganger
		"doppelganger_title":      "Doppelgänger: Wähle deine Identität",
		"doppelganger_became":     "Du bist zu %s geworden (kopiert von %s).",
		"doppelganger_selected":   "Ausgewählt: %s. Klicke zum Abwählen oder bestätige unten.",
		"doppelganger_choose":     "Wähle einen Spieler. Du wirst heimlich seine Rolle für den Rest des Spiels annehmen.",
		"btn_doppelganger_become": "🎭 Werden",

		// Day phase
		"no_deaths_last_night":   "Das Dorf erwacht. Letzte Nacht ist niemand gestorben.",
		"hunter_shot_killed":     "🏹 Der letzte Schuss des Jägers tötete %s!",
		"hunter_victim_was":      "Sie waren ein %s.",
		"hunter_last_shot":       "Dein letzter Schuss",
		"hunter_eliminated_desc": "Du wurdest eliminiert! Wähle einen Spieler, den du mitnimmst, und bestätige dann.",
		"btn_hunter_shoot":       "🏹 Schießen",
		"hunter_choosing":        "Der Jäger wählt sein letztes Ziel...",
		"vote_to_eliminate":      "Abstimmung zur Eliminierung",
		"choose_to_eliminate":    "Wähle einen Spieler zur Eliminierung, oder passe. Mehrheit erforderlich.",
		"dead_cannot_vote":       "Du bist tot und kannst nicht abstimmen.",
		"card_alive":             "Am Leben",
		"card_dead":              "Tot",

		// Role names and descriptions (for player cards)
		"role_name_Villager":     "Dorfbewohner",
		"role_name_Werewolf":     "Werwolf",
		"role_name_Seer":         "Seherin",
		"role_name_Doctor":       "Doktor",
		"role_name_Witch":        "Hexe",
		"role_name_Hunter":       "Jäger",
		"role_name_Cupid":        "Amor",
		"role_name_Guard":        "Wächter",
		"role_name_Mason":        "Freimaurer",
		"role_name_Wolf Cub":     "Wolfsjunges",
		"role_name_Doppelganger": "Doppelgänger",
		"role_desc_Villager":     "Keine besonderen Fähigkeiten, setzt auf Schlussfolgerungen und Diskussionen.",
		"role_desc_Werewolf":     "Kennt andere Werwölfe, stimmt nachts ab, um Dorfbewohner zu töten.",
		"role_desc_Seer":         "Kann jede Nacht einen Spieler untersuchen, um herauszufinden, ob er ein Werwolf ist.",
		"role_desc_Doctor":       "Kann jede Nacht einen Spieler vor einem Werwolfangriff schützen.",
		"role_desc_Witch":        "Hat einen Heiltrank und einen Gifttrank, die sie einmal im Spiel einsetzen kann.",
		"role_desc_Hunter":       "Wenn er eliminiert wird, kann er sofort einen Spieler töten.",
		"role_desc_Cupid":        "In der ersten Nacht wählt er zwei Spieler, die sich verlieben.",
		"role_desc_Guard":        "Schützt jede Nacht einen Spieler, aber nicht zweimal hintereinander denselben.",
		"role_desc_Mason":        "Kennt andere Freimaurer und weiß damit, wer unschuldig ist.",
		"role_desc_Wolf Cub":     "Wenn das Wolfsjunge eliminiert wird, töten die Werwölfe in der nächsten Nacht zwei Opfer.",
		"role_desc_Doppelganger": "In der ersten Nacht kopiert er heimlich die Rolle eines anderen Spielers und übernimmt diese für den Rest des Spiels.",

		// Finished screen
		"victors":            "Sieger",
		"the_fallen":         "Die Gefallenen",
		"btn_play_again":     "Nochmal spielen",
		"villagers_win_alt":  "Dorfbewohner gewinnen",
		"lovers_win_alt":     "Liebende gewinnen",
		"werewolves_win_alt": "Werwölfe gewinnen",

		// Error/toast messages
		"err_game_name_required":   "Spielname ist erforderlich",
		"err_name_required":        "Name ist erforderlich",
		"err_name_taken":           "Name bereits vergeben. Verwende die Anmeldung mit Geheimcode, wenn du das bist.",
		"err_something_wrong":      "Etwas ist schiefgelaufen",
		"err_name_code_required":   "Name und Geheimcode sind erforderlich",
		"err_invalid_credentials":  "Ungültiger Name oder Geheimcode",
		"err_failed_get_game":      "Spiel konnte nicht geladen werden",
		"err_game_already_started": "Rollen können nicht geändert werden: Spiel bereits gestartet",
		"err_game_started":         "Spiel bereits gestartet",
		"err_failed_get_players":   "Spieler konnten nicht geladen werden",
		"err_failed_get_roles":     "Rollenkonfiguration konnte nicht geladen werden",
		"err_role_count_mismatch":  "Rollenanzahl muss Spieleranzahl entsprechen",
		"err_failed_assign_joker":  "Joker-Rolle konnte nicht zugewiesen werden",
		"err_failed_assign_roles":  "Rollen konnten nicht zugewiesen werden",
		"err_failed_start_game":    "Spiel konnte nicht gestartet werden",
		"err_game_not_finished":    "Das Spiel ist noch nicht beendet",
		"err_failed_role_config":   "Rollenkonfiguration konnte nicht geladen werden",
		"err_failed_create_game":   "Neues Spiel konnte nicht erstellt werden",
	},
}

// T looks up a translation key for the given language and optionally formats it.
// Falls back to English, then to the key itself.
func T(lang, key string, args ...interface{}) string {
	lookup := func(l string) (string, bool) {
		if m, ok := translations[l]; ok {
			if s, ok := m[key]; ok {
				return s, true
			}
		}
		return "", false
	}
	s, ok := lookup(lang)
	if !ok {
		s, ok = lookup("en")
	}
	if !ok {
		return key
	}
	if len(args) > 0 {
		return fmt.Sprintf(s, args...)
	}
	return s
}

// getLangFromCookie reads the "lang" cookie and returns "en" or "de".
func getLangFromCookie(r *http.Request) string {
	c, err := r.Cookie("lang")
	if err != nil || (c.Value != "en" && c.Value != "de") {
		return "en"
	}
	return c.Value
}
