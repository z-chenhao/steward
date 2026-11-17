package sqlite

import (
	"context"

	accounts "github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
	appinterface "github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	logos "github.com/z-chenhao/assistant/internal/plugin/appslogos/contract"
)

// One read freezes the five interface migration boundaries with the selected graph.
func (s *Store) AppsInterfaceSelection(ctx context.Context) (revision string, profile, composition, card, sidebar bool, markup int, err error) {
	err = s.db.QueryRowContext(ctx, `WITH activated AS (
 SELECT json_extract(s.value,'$.contract') AS contract
 FROM rsi_activation a JOIN rsi_graph g ON g.revision=json_extract(a.receipt_json,'$.graph_revision'),
 json_each(g.graph_json,'$.bindings') b JOIN rsi_package p ON p.digest=json_extract(b.value,'$.package_digest'),
 json_each(p.manifest_json,'$.provides') s)
 SELECT coalesce(c.active_graph,''),EXISTS(SELECT 1 FROM activated WHERE contract IN (?,?,?)),EXISTS(SELECT 1 FROM activated WHERE contract IN (?,?,?)),EXISTS(SELECT 1 FROM activated WHERE contract=?),EXISTS(SELECT 1 FROM activated WHERE contract=?),CASE WHEN EXISTS(SELECT 1 FROM activated WHERE contract=?) THEN 7 WHEN EXISTS(SELECT 1 FROM activated WHERE contract=?) THEN 6 WHEN EXISTS(SELECT 1 FROM activated WHERE contract=?) THEN 5 WHEN EXISTS(SELECT 1 FROM activated WHERE contract=?) THEN 4 WHEN EXISTS(SELECT 1 FROM activated WHERE contract=?) THEN 3 WHEN EXISTS(SELECT 1 FROM activated WHERE contract=?) THEN 2 WHEN EXISTS(SELECT 1 FROM activated WHERE contract=?) THEN 1 ELSE 0 END
 FROM rsi_control c WHERE c.singleton=1`, appinterface.Interface, appinterface.LocalizedInterface, appinterface.BrowserProfileInterface, appinterface.CompositionInterface, appinterface.LocalizedComposition, appinterface.AccountCompositionInterface, appinterface.CardViewInterface, appinterface.SidebarViewInterface, appinterface.PagesMarkupInterface, appinterface.DirectoryControlsMarkupInterface, appinterface.SettingsMarkupInterface, appinterface.ConnectMarkupInterface, appinterface.AccountMarkupInterface, appinterface.GraphicMarkupInterface, appinterface.MarkupInterface).Scan(&revision, &profile, &composition, &card, &sidebar, &markup)
	return
}

// AppsLogoSelection is independent of account/editorial availability and never
// creates a production Run or writes a projection from an HTTP GET.
func (s *Store) AppsLogoSelection(ctx context.Context) (string, bool, error) {
	return s.ownerReadServiceSelection(ctx, logos.Logo)
}

// AppsEditorialSelection captures the current owner-read graph and durable
// migration boundary in one SQL snapshot. Public editorial reads carry no
// account/query/body facts and create no synthetic production Run.
func (s *Store) AppsEditorialSelection(ctx context.Context) (revision string, managed bool, err error) {
	return s.ownerReadServiceSelection(ctx, contract.Editorial)
}

// AppsAccountPolicies snapshots all durable migration boundaries and the
// current graph together; one owner action cannot mix independent graph reads.
func (s *Store) AppsAccountPolicies(ctx context.Context) (revision string, intent, auth, session, check, close, reconnect bool, err error) {
	err = s.db.QueryRowContext(ctx, `WITH activated AS (
 SELECT json_extract(s.value,'$.contract') AS contract
 FROM rsi_activation a JOIN rsi_graph g ON g.revision=json_extract(a.receipt_json,'$.graph_revision'),
 json_each(g.graph_json,'$.bindings') b JOIN rsi_package p ON p.digest=json_extract(b.value,'$.package_digest'),
 json_each(p.manifest_json,'$.provides') s)
 SELECT coalesce(c.active_graph,''),EXISTS(SELECT 1 FROM activated WHERE contract=?),EXISTS(SELECT 1 FROM activated WHERE contract=?),EXISTS(SELECT 1 FROM activated WHERE contract=?),EXISTS(SELECT 1 FROM activated WHERE contract=?),EXISTS(SELECT 1 FROM activated WHERE contract=?),EXISTS(SELECT 1 FROM activated WHERE contract=?)
 FROM rsi_control c WHERE c.singleton=1`, accounts.ConnectIntent, accounts.AuthConfig, accounts.SessionFlow, accounts.AccountCheck, accounts.AccountClose, accounts.Reconnect).Scan(&revision, &intent, &auth, &session, &check, &close, &reconnect)
	return
}

func (s *Store) ownerReadServiceSelection(ctx context.Context, service string) (revision string, managed bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT coalesce(c.active_graph,''),EXISTS(
SELECT 1 FROM rsi_activation a
JOIN rsi_graph g ON g.revision=json_extract(a.receipt_json,'$.graph_revision'),
json_each(g.graph_json,'$.bindings') b
JOIN rsi_package p ON p.digest=json_extract(b.value,'$.package_digest'),
json_each(p.manifest_json,'$.provides') s
WHERE json_extract(s.value,'$.contract')=?)
FROM rsi_control c WHERE c.singleton=1`, service).Scan(&revision, &managed)
	return
}

// AppsAccountCloseSelection is the independent owner-control consumer: unrelated
// service failures cannot change this exact durable boundary.
func (s *Store) AppsAccountCloseSelection(ctx context.Context) (string, bool, error) {
	return s.ownerReadServiceSelection(ctx, accounts.AccountClose)
}

// AppsSettingsPolicies captures editor and auth selection from one snapshot.
func (s *Store) AppsSettingsPolicies(ctx context.Context) (revision string, editor, auth bool, err error) {
	err = s.db.QueryRowContext(ctx, `WITH activated AS (
 SELECT json_extract(s.value,'$.contract') AS contract
 FROM rsi_activation a JOIN rsi_graph g ON g.revision=json_extract(a.receipt_json,'$.graph_revision'),
 json_each(g.graph_json,'$.bindings') b JOIN rsi_package p ON p.digest=json_extract(b.value,'$.package_digest'),
 json_each(p.manifest_json,'$.provides') s)
 SELECT coalesce(c.active_graph,''),EXISTS(SELECT 1 FROM activated WHERE contract=?),EXISTS(SELECT 1 FROM activated WHERE contract=?)
 FROM rsi_control c WHERE c.singleton=1`, accounts.Settings, accounts.AuthConfig).Scan(&revision, &editor, &auth)
	return
}

// Independent account presentation has its own durable activation boundary.
func (s *Store) AppsAccountInventorySelection(ctx context.Context) (string, bool, error) {
	return s.ownerReadServiceSelection(ctx, accounts.Inventory)
}
