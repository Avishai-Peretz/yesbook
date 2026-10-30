// Command migrate applies the database migrations. Run it as a Cloud Run job
// before deploying a release that needs a new schema.
package main

import (
	"fmt"
	"os"

	"github.com/avi-p/yesbook/internal/adapter/postgres"
)

func main() {
	url := os.Getenv("YESBOOK_DATABASE_URL")
	if url == "" {
		fmt.Fprintln(os.Stderr, "YESBOOK_DATABASE_URL is required")
		os.Exit(2)
	}
	if err := postgres.Migrate(url); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("migrations applied")
}
