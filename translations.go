package main

import (
	"fmt"
	"net/http"
	"strings"
)

var translations = map[string]map[string]string{
	"en": {
		"lang_name": "English",

		// Index page
		"page_title_index":        "Werewolf - Sign In",
		"join_game_heading":       "Join Game",
		"game_name_label":         "Game Name",
		"game_name_placeholder":   "Enter game name",
		"btn_join":                "Join Game",
		"btn_logout":              "Logout",
		"your_games_heading":      "Your Games",
		"game_status_lobby":       "Waiting for players",
		"you_won":                 "you won",
		"you_lost":                "you lost",
		"signin_heading":          "Sign In",
		"name_placeholder":        "Enter your name",
		"name_label":              "Name",
		"secret_code_label":       "Secret Code",
		"secret_code_placeholder": "Your secret code",
		"btn_login":               "Login",
		"btn_signin_continue":     "Continue",

		// Sidebar
		"sidebar_players": "Players",
		"ai_features":     "AI features",
		"narrator_label":  "Narrator",
		"code_label":      "Code",
		"night_round":     "Night %d",
		"day_round":       "Day %d",

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
		"card_unknown":           "Unknown",

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
		"role_name_Joker":        "Joker",
		"role_desc_Villager":     "No special powers — votes by deduction.",
		"role_desc_Werewolf":     "Knows other werewolves, kills nightly.",
		"role_desc_Seer":         "Investigates a player's role each night.",
		"role_desc_Doctor":       "Protects one player each night from attack.",
		"role_desc_Witch":        "One heal potion, one poison potion to use.",
		"role_desc_Hunter":       "Shoots one player when eliminated.",
		"role_desc_Cupid":        "Picks two lovers on night one.",
		"role_desc_Guard":        "Protects one player nightly, no repeats.",
		"role_desc_Mason":        "Knows the other masons.",
		"role_desc_Wolf Cub":     "If killed, werewolves kill two next night.",
		"role_desc_Doppelganger": "Copies another player's role on night one.",
		"role_desc_Joker":        "Secretly assigned a random role at start.",

		// Finished screen
		"victors":            "Victors",
		"the_fallen":         "The Fallen",
		"btn_play_again":     "Play Again",
		"villagers_win_alt":  "Villagers win",
		"lovers_win_alt":     "Lovers win",
		"werewolves_win_alt": "Werewolves win",

		// Error/toast messages
		"err_name_required":        "Name is required",
		"err_name_taken":           "Name already taken. Use login with secret code if this is you.",
		"err_something_wrong":      "Something went wrong",
		"err_invalid_credentials":  "Invalid name or secret code",
		"err_failed_get_game":      "Failed to get game",
		"err_game_already_started": "Cannot update roles: game already started",
		"err_game_started":         "Game already started",
		"err_game_in_progress":     "This game is already in progress — you can't join it now.",
		"err_failed_get_players":   "Failed to get players",
		"err_failed_get_roles":     "Failed to get role configuration",
		"err_role_count_mismatch":  "Role count must match player count",
		"err_failed_assign_joker":  "Failed to assign Joker role",
		"err_failed_assign_roles":  "Failed to assign roles",
		"err_failed_start_game":    "Failed to start game",
		"err_game_not_finished":    "Game is not finished yet",
		"err_failed_role_config":   "Failed to get role config",
		"err_failed_create_game":   "Failed to create new game",

		// Night survey labels
		"survey_prefix":   "Night %v: %s — %s",
		"survey_suspects": "Suspects",
		"survey_theory":   "Theory",
		"survey_notes":    "Notes",

		// History bar and entries
		"hist_heading":          "History",
		"hist_wolf_vote":        "Night %s: %s voted to kill %s",
		"hist_wolf_vote_cub":    "Night %s: %s voted to kill %s (Wolf Cub revenge)",
		"hist_wolf_pass":        "Night %s: %s passed",
		"hist_wolf_pass_2":      "Night %s: %s passed (second kill)",
		"hist_found_dead":       "Night %s: %s (%s) was found dead",
		"hist_protected":        "Night %s: You protected %s",
		"hist_seer_wolf":        "Night %s: You investigated %s — they are a werewolf",
		"hist_seer_not_wolf":    "Night %s: You investigated %s — they are not a werewolf",
		"hist_witch_heal":       "Night %s: You saved %s with your heal potion",
		"hist_witch_poison":     "Night %s: You poisoned %s",
		"hist_witch_confirmed":  "Night %s: Witch %s confirmed her actions",
		"hist_cupid_lover":      "Night 1: Your lover is %s",
		"hist_doppelganger":     "Night 1: You secretly became a %s (copied from %s)",
		"hist_heartbreak_night": "Night %s: %s died of heartbreak after their lover %s was killed",
		"hist_heartbreak_day":   "Day %s: %s died of heartbreak after their lover %s was killed",
		"hist_day_vote":         "Day %s: %s voted to eliminate %s",
		"hist_day_pass":         "Day %s: %s passed",
		"hist_eliminated":       "Day %s: %s (%s) was eliminated by the village",
		"hist_hunter_shot":      "Day %s: Hunter %s shot %s",

		// TTS narrator announcements (fixed game events)
		"tts_game_begins":    "The game begins. Night falls upon the village.",
		"tts_night_falls":    "Night %d falls upon the village.",
		"tts_wolves_chosen":  "The werewolves have made their choice. Silence falls over the village.",
		"tts_dawn_unscathed": "Dawn breaks. The village survived the night unscathed.",
		"tts_dawn_deaths":    "Dawn breaks. The village awakens to find %s dead.",
		"tts_join_and":       " and ",
		"tts_villagers_win":  "The villagers have triumphed! All werewolves have been eliminated.",
		"tts_werewolves_win": "The werewolves have won! They now rule the village.",
		"tts_lovers_win":     "The lovers have won. They are the last ones standing, bound together forever.",
	},
	"de": {
		"lang_name": "Deutsch",

		// Index page
		"page_title_index":        "Werwolf - Anmelden",
		"join_game_heading":       "Spiel beitreten",
		"game_name_label":         "Spielname",
		"game_name_placeholder":   "Spielname eingeben",
		"btn_join":                "Beitreten",
		"btn_logout":              "Abmelden",
		"your_games_heading":      "Deine Spiele",
		"game_status_lobby":       "Wartet auf Mitspieler",
		"you_won":                 "du hast gewonnen",
		"you_lost":                "du hast verloren",
		"signin_heading":          "Anmelden",
		"name_placeholder":        "Name eingeben",
		"name_label":              "Name",
		"secret_code_label":       "Geheimcode",
		"secret_code_placeholder": "Dein Geheimcode",
		"btn_login":               "Anmelden",
		"btn_signin_continue":     "Weiter",

		// Sidebar
		"sidebar_players": "Spieler",
		"ai_features":     "KI-Funktionen",
		"narrator_label":  "Erzähler",
		"code_label":      "Code",
		"night_round":     "Nacht %d",
		"day_round":       "Tag %d",

		// Lobby
		"players_label":     "Spieler:",
		"roles_label":       "Rollen:",
		"ready_to_start":    "Alles bereit!",
		"need_more_players": "Es fehlen noch %d Spieler",
		"need_more_roles":   "Es fehlen noch %d Rollen",
		"configure_roles":   "Rollen unten festlegen",
		"roles_heading":     "Rollen",
		"roles_desc":        "Lege fest, welche Rollen mitspielen.",
		"btn_start_game":    "Spiel starten",

		// Night general
		"waiting_for_players": "Warte auf %d weitere Spieler...",
		"you_are_dead_night":  "Du bist tot. Das Dorf schläft.",
		"village_sleeps":      "Das Dorf schläft...",
		"close_eyes":          "Schließe die Augen und warte auf den Morgen.",
		"storyteller_asking":  "Der Erzähler fragt dich",
		"who_is_werewolf":     "Wer glaubst du, ist ein Werwolf?",
		"how_victim_died":     "Wie glaubst du, ist das Opfer gestorben?",
		"optional":            "(optional)",
		"notes_label":         "Notizen",
		"btn_continue":        "Weiter →",

		// Night: Werewolf
		"werewolf_title":       "Werwolf: Wähle ein Opfer",
		"vote_locked_waiting":  "Du hast abgestimmt. Warte, bis die Nacht endet...",
		"werewolf_select_desc": "Wähle dein Opfer oder passe. Sind alle Wölfe fertig, beende die Abstimmung.",
		"btn_pass":             "Passen",
		"btn_end_vote":         "Abstimmung beenden",
		"vote_pass":            "Passen",
		"wolf_cub_title":       "Rache des Wolfsjungen – zweites Opfer",
		"vote2_locked":         "Zweite Stimme abgegeben. Warte, bis die Nacht endet...",
		"wolf_cub_desc":        "Das Wolfsjunge wurde getötet. Wähle heute Nacht ein zweites Opfer oder passe.",
		"btn_end_second_vote":  "Zweite Abstimmung beenden",

		// Night: Seer
		"seer_title":        "Seherin: Sieh jemandes wahre natur.",
		"seer_already_done": "Du hast heute Nacht schon gesehen.",
		"seer_choose":       "Wen willst du heute Nacht beobachten?",
		"btn_investigate":   "🔮 Sehen",

		// Night: Doctor
		"doctor_title":       "Doktor: Heile einen Spieler",
		"doctor_protecting":  "Du heilst heute Nacht %s.",
		"doctor_choose":      "Wen willst du heute Nacht heilen?",
		"btn_doctor_protect": "🩺 Heilen",

		// Night: Guard
		"guard_title":       "Wächter: Dein Schutz",
		"guard_protecting":  "Du beschützt heute Nacht %s.",
		"guard_choose":      "Wen willst du heute Nacht beschützen?",
		"btn_guard_protect": "🛡️ Beschützen",

		// Night: Witch
		"witch_title":         "Hexe: Deine Tränke",
		"witch_saved":         "✓ Du hast %s geheilt.",
		"witch_poisoned":      "☠️ Du hast %s vergiftet.",
		"witch_waiting":       "Warte, bis die Nacht endet...",
		"heal_potion":         "🧪 Heiltrank",
		"witch_targeting":     "Die Werwölfe greifen ihr Opfer an. Rette es mit deinem Heiltrank:",
		"witch_no_target":     "Die Werwölfe haben noch kein Opfer gewählt...",
		"heal_potion_used":    "Dein Heiltrank ist verbraucht.",
		"poison_potion":       "☠️ Gifttrank",
		"witch_poison_choose": "Wen möchtest du vergiften?",
		"poison_potion_used":  "Dein Gifttrank ist verbraucht.",
		"btn_witch_done":      "✓ Für heute Nacht fertig",

		// Night: Mason
		"mason_title":      "Freimaurer: Deine Brüder",
		"mason_know_these": "Diesen Dorfbewohnern kannst du vertrauen:",
		"mason_alone":      "Du bist der einzige Freimaurer.",

		// Night: Cupid
		"cupid_title":      "Amor: Wähle zwei Liebende",
		"cupid_linked":     "Du hast %s und %s als Liebende verbunden.",
		"cupid_chosen_two": "%s und %s werden ein Paar. Du kannst deine Wahl noch ändern.",
		"cupid_one_chosen": "Erster Liebender gewählt. Wähle den zweiten.",
		"cupid_choose_two": "Wähle zwei Spieler als Liebespaar. Sie erfahren, wer der andere ist.",
		"btn_cupid_link":   "💞 Liebende verbinden",

		// Night: Doppelganger
		"doppelganger_title":      "Doppelgänger: Wähle deine Identität",
		"doppelganger_became":     "Deine neue Rolle: %s (kopiert von %s).",
		"doppelganger_selected":   "Du hast %s gewählt. Du kannst deine Wahl noch ändern.",
		"doppelganger_choose":     "Wähle einen Spieler. Du wirst heimlich seine Rolle für den Rest des Spiels annehmen.",
		"btn_doppelganger_become": "🎭 Werden",

		// Day phase
		"no_deaths_last_night":   "Das Dorf erwacht. In der letzten Nacht ist niemand gestorben.",
		"hunter_shot_killed":     "🏹 Der letzte Schuss des Jägers tötete %s!",
		"hunter_victim_was":      "Die Rolle: %s.",
		"hunter_last_shot":       "Dein letzter Schuss",
		"hunter_eliminated_desc": "Es hat dich erwischt! Wen nimmst du mit in den Tod?",
		"btn_hunter_shoot":       "🏹 Schießen",
		"hunter_choosing":        "Der Jäger wählt sein letztes Ziel...",
		"vote_to_eliminate":      "Wer muss sterben?",
		"choose_to_eliminate":    "Für wen stimmst du? Oder passe – es braucht eine Mehrheit.",
		"dead_cannot_vote":       "Du bist tot und kannst nicht abstimmen.",
		"card_alive":             "Am Leben",
		"card_dead":              "Tot",
		"card_unknown":           "Unbekannt",

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
		"role_name_Joker":        "Joker",
		"role_desc_Villager":     "Nur Verstand zählt, keine Sonderkraft.",
		"role_desc_Werewolf":     "Jagt nachts an der Seite der Wölfe.",
		"role_desc_Seer":         "Erkennt nachts die wahre Natur eines Spielers.",
		"role_desc_Doctor":       "Bewahrt nachts einen Spieler vor dem Biss.",
		"role_desc_Witch":        "Braut einen Heil- und einen Gifttrank.",
		"role_desc_Hunter":       "Reißt im Sterben einen Mitspieler mit.",
		"role_desc_Cupid":        "Verbindet in der ersten Nacht zwei Herzen.",
		"role_desc_Guard":        "Wacht jede Nacht über einen Spieler.",
		"role_desc_Mason":        "Kennt die Brüder seines Bundes.",
		"role_desc_Wolf Cub":     "Stirbt er, tötet das Rudel doppelt.",
		"role_desc_Doppelganger": "Übernimmt in Nacht eins eine fremde Rolle.",
		"role_desc_Joker":        "Eine vom Zufall bestimmte, geheime Rolle.",

		// Finished screen
		"victors":            "Sieger",
		"the_fallen":         "Die Gefallenen",
		"btn_play_again":     "Nochmal spielen",
		"villagers_win_alt":  "Dorfbewohner gewinnen",
		"lovers_win_alt":     "Liebende gewinnen",
		"werewolves_win_alt": "Werwölfe gewinnen",

		// Error/toast messages
		"err_name_required":        "Name ist erforderlich",
		"err_name_taken":           "Name bereits vergeben. Wenn das du bist, melde dich mit deinem Geheimcode an.",
		"err_something_wrong":      "Etwas ist schiefgelaufen",
		"err_invalid_credentials":  "Ungültiger Name oder Geheimcode",
		"err_failed_get_game":      "Spiel konnte nicht geladen werden",
		"err_game_already_started": "Rollen können nicht geändert werden: Spiel bereits gestartet",
		"err_game_started":         "Spiel bereits gestartet",
		"err_game_in_progress":     "Dieses Spiel läuft bereits — du kannst jetzt nicht mehr beitreten.",
		"err_failed_get_players":   "Spieler konnten nicht geladen werden",
		"err_failed_get_roles":     "Rollenkonfiguration konnte nicht geladen werden",
		"err_role_count_mismatch":  "Rollenanzahl muss Spieleranzahl entsprechen",
		"err_failed_assign_joker":  "Joker-Rolle konnte nicht zugewiesen werden",
		"err_failed_assign_roles":  "Rollen konnten nicht zugewiesen werden",
		"err_failed_start_game":    "Spiel konnte nicht gestartet werden",
		"err_game_not_finished":    "Das Spiel ist noch nicht beendet",
		"err_failed_role_config":   "Rollenkonfiguration konnte nicht geladen werden",
		"err_failed_create_game":   "Neues Spiel konnte nicht erstellt werden",

		// Night survey labels
		"survey_prefix":   "Nacht %v: %s — %s",
		"survey_suspects": "Verdächtige",
		"survey_theory":   "Theorie",
		"survey_notes":    "Notizen",

		// History bar and entries
		"hist_heading":          "Verlauf",
		"hist_wolf_vote":        "Nacht %s: %s stimmte dafür, %s zu töten",
		"hist_wolf_vote_cub":    "Nacht %s: %s stimmte dafür, %s zu töten (Rache des Wolfsjungen)",
		"hist_wolf_pass":        "Nacht %s: %s hat gepasst",
		"hist_wolf_pass_2":      "Nacht %s: %s hat gepasst (zweites Opfer)",
		"hist_found_dead":       "Nacht %s: %s (%s) wurde tot aufgefunden",
		"hist_protected":        "Nacht %s: Du hast %s beschützt",
		"hist_seer_wolf":        "Nacht %s: Du hast %s einen Werwolf gesehen.",
		"hist_seer_not_wolf":    "Nacht %s: Du hast %s einen Dorfbewohner gesehen.",
		"hist_witch_heal":       "Nacht %s: Du hast %s mit deinem Heiltrank gerettet",
		"hist_witch_poison":     "Nacht %s: Du hast %s vergiftet",
		"hist_witch_confirmed":  "Nacht %s: Hexe %s hat gehandelt",
		"hist_cupid_lover":      "Nacht 1: Du bist in %s verliebt",
		"hist_doppelganger":     "Nacht 1: Deine geheime Rolle: %s (kopiert von %s)",
		"hist_heartbreak_night": "Nacht %s: %s starb aus Liebeskummer, nachdem %s getötet wurde",
		"hist_heartbreak_day":   "Tag %s: %s starb aus Liebeskummer, nachdem %s getötet wurde",
		"hist_day_vote":         "Tag %s: %s stimmte dafür, %s zu eliminieren",
		"hist_day_pass":         "Tag %s: %s hat gepasst",
		"hist_eliminated":       "Tag %s: %s (%s) wurde vom Dorf eliminiert",
		"hist_hunter_shot":      "Tag %s: Jäger %s erschoss %s",

		// TTS narrator announcements (fixed game events)
		"tts_game_begins":    "Das Spiel beginnt. Die Nacht legt sich über das Dorf.",
		"tts_night_falls":    "Nacht %d legt sich über das Dorf.",
		"tts_wolves_chosen":  "Die Werwölfe haben ihre Wahl getroffen. Stille legt sich über das Dorf.",
		"tts_dawn_unscathed": "Der Morgen graut. Das Dorf hat die Nacht unversehrt überstanden.",
		"tts_dawn_deaths":    "Der Morgen graut. Das Dorf erwacht und findet %s tot vor.",
		"tts_join_and":       " und ",
		"tts_villagers_win":  "Die Dorfbewohner haben triumphiert! Alle Werwölfe wurden ausgelöscht.",
		"tts_werewolves_win": "Die Werwölfe haben gewonnen! Sie beherrschen nun das Dorf.",
		"tts_lovers_win":     "Die Liebenden haben gewonnen. Sie sind die Letzten, für immer miteinander verbunden.",
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

// getLangFromCookie reads the "lang" cookie, falling back to Accept-Language.
// Returns "en" or "de".
func getLangFromCookie(r *http.Request) string {
	c, err := r.Cookie("lang")
	if err == nil && (c.Value == "en" || c.Value == "de") {
		return c.Value
	}
	// No valid cookie — detect from browser Accept-Language header.
	for _, tag := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		lang := strings.ToLower(strings.TrimSpace(strings.SplitN(tag, ";", 2)[0]))
		if strings.HasPrefix(lang, "de") {
			return "de"
		}
		if strings.HasPrefix(lang, "en") {
			return "en"
		}
	}
	return "en"
}
