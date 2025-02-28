package main

import (
	"errors"
	"fmt"
	"strconv"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/jmoiron/sqlx"
	"github.com/jmoiron/sqlx/reflectx"
	"github.com/kelseyhightower/envconfig"
	"github.com/rdbell/relampago"
	rc "github.com/rdbell/relampago/connect"
	"github.com/rdbell/relayer"
)

type ExpensiveRelay struct {
	Domain           string `envconfig:"DOMAIN"`
	PostgresDatabase string `envconfig:"POSTGRESQL_DATABASE"`
	IndexTemplate    string `envconfig:"INDEX_TEMPLATE" default:"./templates/index_example.html.tmpl"`
	InvoiceTemplate  string `envconfig:"INVOICE_TEMPLATE" default:"./templates/invoice_example.html.tmpl"`
	PriceSats        string `envconfig:"PRICE_SATS" default:"500"`
	TelegramAPIKey   string `envconfig:"TELEGRAM_API_KEY" default:""`
	TelegramChatID   string `envconfig:"TELEGRAM_CHAT_ID" default:""`

	LightningBackendSettings rc.LightningBackendSettings

	db  *sqlx.DB
	ln  relampago.Wallet
	bot *tgbotapi.BotAPI
}

func (relay *ExpensiveRelay) Name() string {
	return "ExpensiveRelay"
}

func (relay *ExpensiveRelay) Init() error {
	err := envconfig.Process("", relay)
	if err != nil {
		return fmt.Errorf("couldn't process envconfig: %w", err)
	}
	priceSats, err = strconv.ParseInt(relay.PriceSats, 10, 64)
	if priceSats < 1 || err != nil {
		return errors.New("PRICE_SATS should be an integer above 0")
	}

	err = envconfig.Process("", &relay.LightningBackendSettings)
	if err != nil {
		return fmt.Errorf("couldn't process envconfig: %w", err)
	}

	if db, err := initDB(relay.PostgresDatabase); err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	} else {
		db.Mapper = reflectx.NewMapperFunc("json", sqlx.NameMapper)
		relay.db = db
	}

	// Telegram API
	if relay.TelegramAPIKey != "" && relay.TelegramChatID != "" {
		relay.bot, _ = tgbotapi.NewBotAPI(relay.TelegramAPIKey)
	}

	// lightning
	relay.ln, err = rc.Connect(relay.LightningBackendSettings)
	if err != nil {
		return fmt.Errorf("failed to connect to lightning backend: %w", err)
	}

	// getting notified of invoice payments
	stream, err := relay.ln.PaidInvoicesStream()
	if err != nil {
		return fmt.Errorf("failed to listen for incoming payments: %w", err)
	}
	go func() {
		for status := range stream {
			handlePaidInvoice(status)
		}
	}()

	// endpoints
	relayer.Router.Path("/").HandlerFunc(handleWebpage)
	relayer.Router.Path("/invoice").HandlerFunc(handleLnurlRegisterHTMLResponse)
	relayer.Router.Path("/.well-known/lnurlp/{pubkey}").HandlerFunc(handleLnurlRegisterJSONResponse)
	relayer.Router.Path("/check_registration/{pubkey}").HandlerFunc(handleCheckRegistration)

	// cleanup events
	go cleanupRoutine()

	return nil
}

var relay = ExpensiveRelay{}

func main() {
	relayer.Start(&relay)
}
