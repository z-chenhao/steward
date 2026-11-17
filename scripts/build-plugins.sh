#!/bin/sh
# Build Steward-owned feature packages without opening a user data root.
set -eu
plugin_host=${1:?Usage: build-plugins.sh /absolute/steward /absolute/output}
plugin_output=${2:?Usage: build-plugins.sh /absolute/steward /absolute/output}
plugin_dependencies=${3:-}
case "$plugin_host:$plugin_output" in /*:/*) ;; *) echo 'Plugin build paths must be absolute.' >&2; exit 1 ;; esac
plugin_source=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
if [ -z "$plugin_dependencies" ]; then plugin_dependencies=$plugin_source/runtime/javascript/node_modules; fi
for plugin_name in docx-reader image-ocr trace-inspector context-compact recall conversation memory-curator evidence-reader skill-library experience-learning task-control task-progress task-feedback delegation codemode file-tools shell-tools coding-workspace computer-use web-access web-extract mcp-bridge connected-apps tool-discovery apps-directory apps-accounts apps-logos apps-interface github-pr-followup artifact-workbench intelligent-ui schedule memory-inspector model-connections feishu-chat capability-settings terminal-interface reference-pricing web-workbench service-watchdog; do
  plugin_directory=$plugin_output/$plugin_name
  mkdir -p "$plugin_directory"
  (cd "$plugin_source" && go build -trimpath -o "$plugin_directory/plugin" "./cmd/plugins/$plugin_name")
  cp "$plugin_source/plugins/$plugin_name/steward.plugin.json" "$plugin_directory/steward.plugin.json"
  if [ "$plugin_name" = web-extract ]; then
    "$plugin_host" plugin parser-bundle "$plugin_source/internal/plugin/webextract/extract.mjs" "$plugin_directory/extract.mjs" "$plugin_dependencies"
  fi
  if [ "$plugin_name" = apps-interface ]; then
    mkdir -p "$plugin_directory/views"
    for app_view in card sidebar markup; do
      cp "$plugin_source/plugins/apps-interface/views/$app_view.json" "$plugin_directory/views/$app_view.json"
    done
  fi
  if [ "$plugin_name" = trace-inspector ]; then
    mkdir -p "$plugin_directory/views"
    cp "$plugin_source/internal/plugin/traceinspector/view/trace-worker.js" "$plugin_directory/views/trace-worker.js"
    cp "$plugin_source/internal/plugin/traceinspector/view/trace-activity.css" "$plugin_directory/views/trace-activity.css"
    cp "$plugin_source/internal/plugin/traceinspector/view/trace.html" "$plugin_directory/views/trace.html"
    cp "$plugin_source/internal/plugin/traceinspector/view/copy.json" "$plugin_directory/views/copy.json"
  fi
  if [ "$plugin_name" = capability-settings ]; then
    mkdir -p "$plugin_directory/view"
    cp "$plugin_source/internal/plugin/capabilityforms/view/capabilities.html" "$plugin_directory/view/capabilities.html"
    cp "$plugin_source/internal/plugin/capabilityforms/view/copy.json" "$plugin_directory/view/copy.json"
  fi
  if [ "$plugin_name" = model-connections ]; then
    mkdir -p "$plugin_directory/views"
    cp "$plugin_source/internal/plugin/modelconnections/view/models.html" "$plugin_directory/views/models.html"
    cp "$plugin_source/internal/plugin/modelconnections/view/copy.json" "$plugin_directory/views/copy.json"
  fi
  if [ "$plugin_name" = memory-inspector ]; then
    mkdir -p "$plugin_directory/views"
    cp "$plugin_source/internal/plugin/memoryinspector/view/memory.html" "$plugin_directory/views/memory.html"
    cp "$plugin_source/internal/plugin/memoryinspector/view/memory.css" "$plugin_directory/views/memory.css"
    cp "$plugin_source/internal/plugin/memoryinspector/view/copy.json" "$plugin_directory/views/copy.json"
    cp "$plugin_source/internal/plugin/memoryinspector/view/memory-worker.js" "$plugin_directory/views/memory-worker.js"
  fi
  if [ "$plugin_name" = intelligent-ui ]; then
    mkdir -p "$plugin_directory/view"
    cp "$plugin_source/internal/interaction/web/assets/widget-runtime.js" "$plugin_directory/view/widget-runtime.js"
    cp "$plugin_source/internal/interaction/web/assets/widget.css" "$plugin_directory/view/widget.css"
    mkdir -p "$plugin_directory/skill"
    cp -R "$plugin_source/internal/plugin/intelligentui/skill/." "$plugin_directory/skill/"
  fi
  "$plugin_host" plugin seal "$plugin_directory" >/dev/null
done
