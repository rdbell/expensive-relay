package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/fiatjaf/go-nostr"
)

func (relay *ExpensiveRelay) SaveEvent(evt *nostr.Event) error {
	// disallow large contents
	if len(evt.Content) > 10000 {
		return errors.New("event content too large")
	}

	// check if user is registered
	var registered bool
	if err := relay.db.Get(&registered, `SELECT true FROM registered_users WHERE pubkey = $1 AND registered_at IS NOT NULL`,
		evt.PubKey); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("%s not registered", evt.PubKey)
		} else {
			return fmt.Errorf("error reading users table: %w", err)
		}
	} else if !registered {
		return fmt.Errorf("%s's invoice was not paid", evt.PubKey)
	}

	go func() {
		// Telegram event notifications
		if relay.bot != nil {
			chatID, _ := strconv.Atoi(relay.TelegramChatID)
			msg := tgbotapi.NewMessage(int64(chatID), string(evt.Serialize()))
			_, _ = relay.bot.Send(msg)
		}
	}()

	// react to different kinds of events
	switch evt.Kind {
	case nostr.KindSetMetadata:
		// delete past set_metadata events from this user
		_, _ = relay.db.Exec(`DELETE FROM event WHERE pubkey = $1 AND kind = 0`, evt.PubKey)
	case nostr.KindRecommendServer:
		// delete past recommend_server events equal to this one
		_, _ = relay.db.Exec(`DELETE FROM event WHERE pubkey = $1 AND kind = 2 AND content = $2`,
			evt.PubKey, evt.Content)
	case nostr.KindContactList:
		// delete past contact lists from this same pubkey
		_, _ = relay.db.Exec(`DELETE FROM event WHERE pubkey = $1 AND kind = 3`, evt.PubKey)
	case nostr.KindDeletion:
		for _, target := range evt.Tags {
			// Validate tag
			if len(target) < 2 {
				break
			}
			if target[0] != "e" {
				break
			}

			// delete target
			_, _ = relay.db.Exec(`DELETE FROM event WHERE pubkey = $1 AND id = $2`, evt.PubKey, target[1])
		}
		return nil
	}

	// insert
	tagsj, _ := json.Marshal(evt.Tags)
	_, err := relay.db.Exec(`
        INSERT INTO event (id, pubkey, created_at, kind, tags, content, sig)
        VALUES ($1, $2, $3, $4, $5, $6, $7)
    `, evt.ID, evt.PubKey, evt.CreatedAt, evt.Kind, tagsj, evt.Content, evt.Sig)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			// already exists
			return nil
		}

		return fmt.Errorf("failed to save event from %s", evt.PubKey)
	}

	return nil
}
