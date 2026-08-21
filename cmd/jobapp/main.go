package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"jobapp/internal/config"
	"jobapp/internal/db"
	"jobapp/internal/llm"
	"jobapp/internal/scrape"
	"jobapp/internal/telegram"
	"jobapp/internal/web"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("jobapp: ")

	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	cmd := os.Args[1]
	switch cmd {
	case "serve":
		os.Exit(runServe(cfg, os.Args[2:]))
	case "crawl":
		os.Exit(runCrawl(cfg, os.Args[2:]))
	case "telegram-check":
		os.Exit(runTelegram(cfg, os.Args[2:]))
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `Usage: jobapp <command> [flags]

Commands:
  serve            Run the web frontend (socket-activated or -listen)
  crawl            One crawl pass over enabled sources, then exit
  telegram-check   One Telegram short-poll pass, then exit

Serve flags:
  -listen ADDR         Listen address when not socket-activated (default :8080)
  -db PATH             SQLite database path
  -idle-timeout DUR    Exit after idle duration (0 disables; e.g. 5m)

Crawl flags:
  -db PATH             SQLite database path
  -rate-min DUR        Min delay between requests to the same host (default 2s)
  -rate-max DUR        Max delay between requests to the same host (default 5s)
                       Set both to 0 to disable rate limiting

`)
}

func runServe(cfg config.Config, args []string) int {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	listen := fs.String("listen", cfg.ListenAddr, "listen address when not socket-activated (e.g. :8080)")
	dbPath := fs.String("db", cfg.DBPath, "SQLite database path")
	idleTimeout := fs.Duration("idle-timeout", 0, "exit after this idle duration (0 disables)")
	_ = fs.Parse(args)

	if cfg.SitePasswordHash == "" {
		log.Fatal("JOBAPP_PASSWORD_HASH is required for serve")
	}
	if cfg.SessionSecret == "" {
		log.Fatal("JOBAPP_SESSION_SECRET is required for serve (generate a random string at deploy time)")
	}

	sqlDB, err := db.Open(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer sqlDB.Close()

	llmClient := llm.NewClient(cfg.OpenRouterAPIKey, cfg.OpenRouterModel, cfg.OpenRouterSystemPrompt)
	srv, err := web.New(sqlDB, cfg.SitePasswordHash, cfg.SessionSecret, llmClient)
	if err != nil {
		log.Fatal(err)
	}
	if err := srv.ListenAndServe(*listen, *idleTimeout); err != nil {
		log.Fatal(err)
	}
	return 0
}

func runCrawl(cfg config.Config, args []string) int {
	fs := flag.NewFlagSet("crawl", flag.ExitOnError)
	dbPath := fs.String("db", cfg.DBPath, "SQLite database path")
	rateMin := fs.Duration("rate-min", 2*time.Second, "min delay between requests to the same host")
	rateMax := fs.Duration("rate-max", 5*time.Second, "max delay between requests to the same host")
	_ = fs.Parse(args)

	sqlDB, err := db.Open(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer sqlDB.Close()

	var limiter scrape.Limiter
	if *rateMin != 0 || *rateMax != 0 {
		limiter, err = scrape.NewHostLimiter(*rateMin, *rateMax)
		if err != nil {
			log.Fatal(err)
		}
	}

	runner := scrape.New(scrape.Options{
		ScrapeConcurrency: cfg.ScrapeConcurrency,
		ChromePath:        cfg.ChromePath,
		Limiter:           limiter,
		LLM:               llm.NewClient(cfg.OpenRouterAPIKey, cfg.OpenRouterModel, cfg.OpenRouterSystemPrompt),
	})
	if _, err := runner.RunCrawl(context.Background(), sqlDB); err != nil {
		log.Fatal(err)
	}
	return 0
}

func runTelegram(cfg config.Config, args []string) int {
	fs := flag.NewFlagSet("telegram-check", flag.ExitOnError)
	dbPath := fs.String("db", cfg.DBPath, "SQLite database path")
	_ = fs.Parse(args)

	sqlDB, err := db.Open(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer sqlDB.Close()

	runner := scrape.New(scrape.Options{
		ScrapeConcurrency: cfg.ScrapeConcurrency,
		ChromePath:        cfg.ChromePath,
		LLM:               llm.NewClient(cfg.OpenRouterAPIKey, cfg.OpenRouterModel, cfg.OpenRouterSystemPrompt),
	})
	client := telegram.NewClient(cfg.TelegramBotToken, cfg.TelegramChatID)
	if _, err := telegram.RunCheck(context.Background(), sqlDB, runner, client); err != nil {
		log.Fatal(err)
	}
	return 0
}
