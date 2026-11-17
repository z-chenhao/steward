# Historical native UI resources

These bytes are the last pre-managed UI resource generation. They are used only
for Runs whose frozen `managed_services_json` does not include
`steward.ui-document/1`, including explicitly unbundled embedders. Managed Runs
must read their own sealed package resources and may never fall back here.

The default31 package builder uses `assets/widget.css` and
`assets/widget-runtime.js`, not this directory. Changes to current plugin source
or generated assets must not regenerate this historical snapshot.

Retain these files while a historical native widget/reference or an explicit
unbundled consumer requires them. Remove the reader and this directory together
when those consumers are retired. This is a private compatibility boundary, not
a second current implementation or a public indefinite compatibility promise.

The original bundled third-party notices remain in `assets/licenses.txt` and
`web/licenses/`; no ownership or attribution is removed by retaining these bytes.
