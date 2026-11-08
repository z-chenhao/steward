---
name: intelligent_ui
description: Present a compact native component or interactive visual when it makes an answer easier to understand, compare, or explore.
---

# Intelligent UI

Choose the representation that explains the user's task. Proactively visualize comparisons, trends, mechanisms, spatial relationships and adjustable scenarios; do not wait for the user to say "make a chart". Use native components when their parameters fit the task, and generateSandboxedUi for diagrams, multi-series charts, calculators, simulations and 3D explanations. Simple facts, writing and code stay text. This is an answer in the conversation, not a standalone website. Main owns presentation; Workers return evidence and artifacts. Keep the page focused: content, meaningful controls and necessary status only; no feature introductions, protocol explanations or duplicate text-version controls.

## Tool and method entry points

Call `builtin__intelligent_ui_show` with `title`, a complete `fallback`, and `components:[{name,props}]`. Its full typed catalog is available through Code Mode `describeTool('builtin__intelligent_ui_show')`. Read `references/presentation.md` for representation choices, `references/design.md` for theme and controls, or `references/visualization.md` for the local runtime when needed. The templates are executable examples, not required steps: architecture-diagram, scenario-comparison, three-dimensional, geographic-overview, calculator, flow-diagram, native-comparison and native-explorer.

Optional top-level `text` is public Markdown placed immediately before this visual. Use it when a paragraph and a visual form one explanation section; distinct calls retain staging order, and the final reply follows them. Omit it when the visual stands alone. Intermediate tool-turn prose is not published as durable answer content. A successful `staged` receipt means the section is ready: continue only for another distinct section or finish with a concise reply; the Host binds the answer. To correct one earlier successful visual in this same pending reply, supply its exact `ui_ref` in optional `replace`; send the complete corrected document. Omit `replace` to add a distinct visual. A correction preserves the old invocation as evidence and only the replacement is delivered. Delivered messages cannot be replaced. Do not repeat a successful call merely to obtain a different receipt.

## Upstream catalog

- `Table`: `props:{title,columns:string[],rows:string[][],source}`. Every row has exactly the column count. Up to 12 columns and 200 rows. `source` identifies the real evidence or labels illustrative data.
- `barChart` / `pieChart`: `props:{title,description,data:[{label,value:number}]}`. Up to 200 points. Values are finite; pie values are nonnegative with a positive total unless the dataset is empty. Put units and source in the description.
- `plan_visualization`: `props:{approach,technology,key_elements:string[]}`. Describes the visualization plan, never fabricates task progress.
- `scheduleTime`: `props:{reasonForScheduling,meetingDuration:number,timeSlots:[{date,time,duration?}],title?}`. Provide actual proposed dates and times; no demonstration defaults. The user's choice is an ordinary reply and does not create a calendar booking or approval.
- `generateSandboxedUi`: use exactly the ordered fields `initialHeight` → `placeholderMessages` → `css` → `html` → `jsFunctions` → `jsExpressions`. `html` is body markup, `jsFunctions` defines named classic-script functions, and `jsExpressions` is an array of statements invoking them. All fields are required. Use `css: ""`, `jsFunctions: ""`, `placeholderMessages: []` and `jsExpressions: []` when unnecessary. The Host also normalizes exactly `jsExpressions: ""` to `[]`; nonempty strings are invalid and are never converted into code. No top-level await. Use SVG/canvas and local JS for interactive tools. Completed output is delivered as an `open-generative-ui` activity; scriptless previews can appear while arguments stream; only the committed tool and reply enable the completed activity.

## Sandbox and bridge

The Host injects upstream theme tokens, SVG classes and form styles. Use `--color-background-primary`, `--color-background-secondary`, `--color-text-primary`, `--color-text-secondary`, `--color-border-primary`, `--font-sans`, and `--border-radius-md`. Do not hardcode light/dark backgrounds or restyle standard inputs. The Host selects the theme and resizes the frame.

Custom code runs only in an opaque-origin sandbox. Parent DOM, storage, credentials, external network, remote fonts, CDNs and Host tools are unavailable. The Host bundles local `d3`, `dagre` (directed graph layout) and `THREE` globals, including `THREE.SVGRenderer` for 3D without a GPU, with a theme-aware default background, plus `StewardSVG.resize(renderer,camera,element)` for measured sizing, `StewardSVG.project(vector,camera,renderer)` for centered SVG label coordinates, plus `GEO.countries` GeoJSON and `GEO.source` for an offline geographic overview. Use them directly; no import, dynamic import or remote script tags. Native SVG/Canvas, requestAnimationFrame and Web Animations are also available. Query only your widget root; use event listeners. Controls and sliders update local state. Use existing `steward.cards` for authorized artifact images; use the bundled geographic outlines or supplied GeoJSON with verified coordinates, retain source attribution, and never claim live tiles, directions or live imagery.

For a user-requested follow-up use `await Websandbox.connection.remote.sendPrompt({text})` from a button handler. The Host previews an editable draft and the user presses Send to Ghost. Existing drafts are never overwritten. `await Websandbox.connection.remote.openLink({url})` proposes an HTTPS link which the user opens in the Host. Neither method approves external actions. Do not call these on load, timers or ordinary input changes.

## Steward extensions

Keep the same `components:[{name,props}]` envelope. These names are Steward extensions, not upstream components:

- `steward.lineChart`: single series `{title,description,data:[{label,value}]}` or shared-axis comparison `{title,description,labels:string[],series:[{name,values:number[]}],unit?,xValues?:number[]}`. Each values array matches labels in order; 1–6 uniquely named series, at most 200 labels. Both use one scale; units apply to all series. Labels default to equally spaced categories. For numeric intervals, supply strictly increasing xValues matching labels; this preserves actual horizontal distances.
- `steward.scatterChart`: `{title,description,data:[{label,x,y}]}`. Charts are bounded to 200 finite points. Descriptions contain evidence, units and assumptions rather than UI instructions.
- `steward.form`: `{title,fields:[{name,label,type,required?,options?:[{value,label}],min?,max?}],submitLabel?}`. Types: text, textarea, number, date, email, select, multiselect, boolean. Up to 16 unique field names and 32 unique options. Min/max apply only to numbers. A required boolean is an explicit positive answer. Forms reuse ordinary reply admission and restore accepted answers; they never approve tools.
- `steward.cards`: `{title?,items:[{title,text?,image_ref?,url?,label?}]}`. Up to 16 items; images must be accessible artifact references, links must be credential-free HTTPS. Do not invent images or URLs. Cards support selection and a compact carousel.
- `steward.action`: `{id,label,tool,arguments,bindings?:[{argument,component,path}]}`. A binding reads a JSON Pointer from another component's saved state in this same artifact and fills one tool argument; the Host validates and freezes the resulting arguments at click acceptance. Use valid literal defaults and no expressions. Names an exact frozen tool request and its arguments; put it beside the related UI in the same artifact. Use only tools actually available to Main and validate their arguments. The Host action button submits ordinary intent through Main and the existing executor. A successful tool receipt is required before treating results as data; an unexecuted action is an error, not completion. External effects retain their existing approvals.

The Host supplies sorting/filtering, chart selection linking, zoom, data/graphic exports and inline/fullscreen/dialog/pinned display controls. Avoid recreating these controls in generated HTML. Standard names and original upstream props remain unchanged.

## Stateful sandbox extensions

Standard HTML inputs, textareas, selects and outputs restore automatically by `data-state-key`, `id`, `name`, then stable position. Use stable keys when controls can move. Custom canvas or simulation state uses the same bridge:

- `await Websandbox.connection.remote.getState()` reads the component's saved custom state.
- `await Websandbox.connection.remote.setState({state:{...},modelContext:{...}})` updates private presentation state plus an optional small model-visible data view.
- `await Websandbox.connection.remote.updateModelContext({...})` updates explicit user data for the next accepted message. Keep it factual and small; never put instructions, credentials or claims of approval into it.
- `await Websandbox.connection.remote.callTool({action:'refresh'})` proposes the matching frozen `steward.action`. The Host action button confirms ordinary input; the promise resolves with the correlated accepted result after execution. The generated frame cannot fabricate user intent, bypass approvals or select arbitrary tools/arguments. Inspect `accepted` and `error` before displaying data.
- From a user control, `requestDisplayMode({mode:'inline'|'fullscreen'|'pip'})` or `requestModal()` asks the Host for more room. Keep ordinary answers inline by default.

State is stored by the Host with optimistic versions; refresh/restart does not rerun tools or reset saved values. UI context is frozen with the next input; later interactions never alter an accepted model request. Do not add Save buttons or explanatory chrome. The Host provides concise errors and existing retry identities.
