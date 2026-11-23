package migrate

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Sawmonabo/workbench/internal/agent/costs"
	"github.com/Sawmonabo/workbench/internal/operation"
)

// priceLists deletes the price lists the costs import downloaded into files
// beside the ledger, whose prices the ledger keeps. It runs after 0007, so it
// finds them where the ledger is. It opens the ledger first, as a report does
// (see [costs.Paths.OpenExisting]), which upgrades a ledger of an older schema
// and fills its prices from those files in the same transaction, so a file is
// never deleted before its prices are rows. With no ledger there is nothing
// to fill: a new ledger downloads its prices, and the files are deleted too.
var priceLists = Migration{
	ID:    "0008-price-lists",
	Title: "Delete the price lists the costs ledger keeps",
	Doing: "deleting the price lists the costs ledger keeps",
	What: "Opens the costs ledger, which keeps the official prices the import downloads and, " +
		"when it is of an older schema, takes them from the price lists beside it, then " +
		"deletes those lists (<tool>/rates-official.json in Workbench's data folder, " +
		"agent/costs/) and each tool folder they leave empty. A ledger that can't be opened " +
		"keeps every list.",
	Recovery: "nothing to undo: the prices are in the ledger, and the import downloads a " +
		"list again once it is a week old",
	Check: func(l Locations) (Status, string) {
		lists := priceListFiles(costs.LocationsIn(l.Home, l.Runtime).Ledger)
		if len(lists) == 0 {
			return Done, ""
		}
		shown := make([]string, len(lists))
		for i, path := range lists {
			shown[i] = operation.ShowPath(l.Home, path)
		}
		return Pending, "deletes " + strings.Join(shown, ", ") +
			" once the ledger holds their prices, and each folder they leave empty"
	},
	Run: func(ctx context.Context, l Locations) ([]string, error) {
		ledger, err := costs.LocationsIn(l.Home, l.Runtime).OpenExisting(ctx)
		if err != nil {
			return nil, err
		}
		// The open may have moved the costs folders under agent/ (0007's move).
		at := costs.LocationsIn(l.Home, l.Runtime).Ledger
		if ledger != nil {
			at = ledger.Path
			if err = ledger.Close(); err != nil {
				return nil, err
			}
		}
		for _, path := range priceListFiles(at) {
			if err = operation.RemoveIfThere(path); err != nil {
				return nil, err
			}
			folder := filepath.Dir(path)
			operation.RemoveStaleWrites(folder)
			if err = operation.RemoveIfThere(folder); err != nil &&
				!errors.Is(err, syscall.ENOTEMPTY) && !errors.Is(err, syscall.EEXIST) {
				return nil, err
			}
		}
		return nil, nil
	},
}

// priceListFiles are the price list files beside the ledger at ledger that a
// run deletes: each that is a plain file, and each that can't be inspected,
// which the run then names.
func priceListFiles(ledger string) []string {
	var lists []string
	for _, path := range costs.PriceLists(ledger) {
		info, err := os.Lstat(path)
		switch {
		case err == nil && info.Mode().IsRegular(),
			err != nil && !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR):
			lists = append(lists, path)
		}
	}
	return lists
}
