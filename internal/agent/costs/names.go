package costs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"github.com/Sawmonabo/workbench/internal/sqlitefile"
)

// loadNames reads every name, by its id: the zero nameKey for an id that is
// none.
func loadNames(ctx context.Context, q querier) ([]nameKey, error) {
	rows, err := q.QueryContext(ctx, "SELECT id, kind, name FROM names ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	names := []nameKey{{}}
	for rows.Next() {
		var id int64
		var key nameKey
		if err := rows.Scan(&id, &key.kind, &key.name); err != nil {
			return nil, err
		}
		if id < 1 {
			continue
		}
		for int64(len(names)) <= id {
			names = append(names, nameKey{})
		}
		names[id] = key
	}
	return names, rows.Err()
}

// nameKey is a name of a kind.
type nameKey struct {
	kind int
	name string
}

// nameCache is the names a Ledger has met in committed transactions, by name
// and by id. A name is never deleted or renamed, so a committed id stays
// right for as long as the ledger exists, whatever other processes write.
type nameCache struct {
	mu     sync.Mutex
	loaded bool
	ids    map[nameKey]int64
	byID   map[int64]string
}

// nameSet resolves names to their ids, and back, inside one transaction: from
// the ledger's cache, which it loads whole on its first use, else from the
// table, adding a name that is missing. The names it added join the cache only
// once the transaction commits (keep): a rolled-back one never stored them.
type nameSet struct {
	ctx         context.Context
	tx          *sql.Tx
	cache       *nameCache
	added       map[nameKey]int64
	addedByID   map[int64]string
	find, write *sql.Stmt
}

func (l *Ledger) nameSet(ctx context.Context, tx *sql.Tx) *nameSet {
	return &nameSet{
		ctx: ctx, tx: tx, cache: &l.names,
		added: map[nameKey]int64{}, addedByID: map[int64]string{},
	}
}

// load fills the cache from the table, once per Ledger. The caller holds
// the cache's lock.
func (n *nameSet) load() error {
	if n.cache.loaded {
		return nil
	}
	names, err := loadNames(n.ctx, n.tx)
	if err != nil {
		return err
	}
	n.cache.ids, n.cache.byID = map[nameKey]int64{}, map[int64]string{}
	for id, key := range names {
		if key.kind != 0 {
			n.cache.ids[key], n.cache.byID[int64(id)] = int64(id), key.name
		}
	}
	n.cache.loaded = true
	return nil
}

// cached is the id the cache or this transaction knows for key.
func (n *nameSet) cached(key nameKey) (int64, bool, error) {
	if id, ok := n.added[key]; ok {
		return id, true, nil
	}
	n.cache.mu.Lock()
	defer n.cache.mu.Unlock()
	if err := n.load(); err != nil {
		return 0, false, err
	}
	id, ok := n.cache.ids[key]
	return id, ok, nil
}

// lookup is the id of name, a name of kind, found false when the ledger
// holds no such name, and so no row that names it.
func (n *nameSet) lookup(kind int, name string) (id int64, found bool, err error) {
	key := nameKey{kind, name}
	if id, ok, err := n.cached(key); ok || err != nil {
		return id, ok, err
	}
	if n.find == nil {
		if n.find, err = n.tx.PrepareContext(n.ctx,
			"SELECT id FROM names WHERE kind = ? AND name = ?"); err != nil {
			return 0, false, err
		}
	}
	switch err = n.find.QueryRowContext(n.ctx, kind, name).Scan(&id); {
	case errors.Is(err, sql.ErrNoRows):
		return 0, false, nil
	case err != nil:
		return 0, false, err
	}
	// Another process committed it since the cache was loaded.
	n.cache.mu.Lock()
	n.cache.ids[key], n.cache.byID[id] = id, name
	n.cache.mu.Unlock()
	return id, true, nil
}

// id is the id of name, a name of kind, which it adds when the ledger holds
// none.
func (n *nameSet) id(kind int, name string) (int64, error) {
	id, found, err := n.lookup(kind, name)
	if found || err != nil {
		return id, err
	}
	if n.write == nil {
		if n.write, err = n.tx.PrepareContext(n.ctx,
			"INSERT INTO names (kind, name) VALUES (?, ?) RETURNING id"); err != nil {
			return 0, err
		}
	}
	if err := n.write.QueryRowContext(n.ctx, kind, name).Scan(&id); err != nil {
		return 0, err
	}
	n.added[nameKey{kind, name}], n.addedByID[id] = id, name
	return id, nil
}

// name is the name whose id is id.
func (n *nameSet) name(id int64) (string, error) {
	if name, ok := n.addedByID[id]; ok {
		return name, nil
	}
	n.cache.mu.Lock()
	defer n.cache.mu.Unlock()
	if err := n.load(); err != nil {
		return "", err
	}
	if name, ok := n.cache.byID[id]; ok {
		return name, nil
	}
	var key nameKey
	if err := n.tx.QueryRowContext(n.ctx, "SELECT kind, name FROM names WHERE id = ?", id).
		Scan(&key.kind, &key.name); err != nil {
		return "", fmt.Errorf("the name %d of a response: %w", id, err)
	}
	// Another process committed it since the cache was loaded.
	n.cache.ids[key], n.cache.byID[id] = id, key.name
	return key.name, nil
}

// found is the id of each of keys that the ledger holds as a name of kind,
// by the name: the others name no row.
func (n *nameSet) found(kind int, keys []string) (map[string]int64, error) {
	ids := map[string]int64{}
	for _, key := range keys {
		id, ok, err := n.lookup(kind, key)
		if err != nil {
			return nil, err
		}
		if ok {
			ids[key] = id
		}
	}
	return ids, nil
}

// keep adds the names this transaction stored to the cache, once it has
// committed.
func (n *nameSet) keep() {
	n.cache.mu.Lock()
	defer n.cache.mu.Unlock()
	for key, id := range n.added {
		n.cache.ids[key], n.cache.byID[id] = id, key.name
	}
}

// transactNames is [sqlitefile.Transact] on the ledger with the transaction's
// nameSet, whose names join the cache once it commits.
func (l *Ledger) transactNames(ctx context.Context, fn func(*sql.Tx, *nameSet) error) error {
	var names *nameSet
	err := sqlitefile.Transact(ctx, l.db, func(tx *sql.Tx) error {
		names = l.nameSet(ctx, tx)
		return fn(tx, names)
	})
	if err == nil {
		names.keep()
	}
	return err
}
