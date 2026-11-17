# Local visualization runtime

The final sandbox supplies `window.d3` (D3 7.9.0) and `window.THREE` (Three.js 0.181.2, with `THREE.SVGRenderer`). It also supplies `GEO.countries` (a GeoJSON FeatureCollection of Natural Earth 4.1.0 country outlines via world-atlas 2.0.2, 1:110m) and `GEO.source` (the visible attribution). These are local, versioned resources installed before your code. Use classic-script globals directly. The ordered generateSandboxedUi fields and Websandbox bridge are unchanged. Previews are scriptless; useful labels and the initial structure belong in html, not exclusively in JavaScript.

- **D3**: scales, axes, shapes, hierarchy/tree layouts, geographic projections, force layouts, drag and zoom. Use local arrays and supplied GeoJSON. `d3.json`, `d3.csv` and remote image/tile loads cannot access the network.
- **Three.js**: scenes, cameras, geometries, materials and transforms. Prefer `new THREE.SVGRenderer()` for small geometric explanations; its default clear color follows the frame theme, while an explicit clear color or scene background remains yours. `StewardSVG.resize(renderer,camera,element)` measures the container and updates a perspective camera; `StewardSVG.project(worldVector,camera,renderer)` returns `{x,y}` in the centered SVG viewBox for labels (do not add half the viewport again). it works without WebGL. Use WebGL only when its value is necessary and catch renderer creation failure with a readable in-widget alternative. Remote models, textures, workers and additional addons are unavailable.
- **Animation**: use requestAnimationFrame or Element.animate. Animate a meaningful change after user input; avoid continuous decoration. Pause when the document is hidden, honor prefers-reduced-motion, and cancel old animation loops before starting another.

Geography uses d3.geoPath and a projection appropriate to the region. Reuse GEO.countries or supplied GeoJSON; never hand-draw real country boundaries. Retain GEO.source visibly when using the bundled outlines. This coarse historical dataset is for geographic context, not street-level precision, live conditions or authoritative boundary decisions. Obtain stop coordinates from evidence, show their names, and label connections illustrative unless backed by directions data. Use authorized artifact cards for photographs; never fabricate a photo URL.

## Responsive and readable

Measure the root's actual inline width with ResizeObserver, not the outer viewport. Compute SVG width and label positions from that measurement. A fixed desktop viewBox scaled to a phone produces unreadable labels: reflow nodes or use a simple vertical layout instead. Keep essential labels at least 12px in their rendered size, reserve room for long labels and negative/large values, and keep text out of connector paths. Titles are already shown by the Host.

Use neutral structure and one semantic accent; add a second series color only when it identifies a persistent alternative. Resolve theme colors with computed styles before passing them to Canvas or Three.js, and redraw after theme changes. Direct-label small numbers of series. Give charts axes, units and meaningful tick labels; do not smooth a line if that implies unsupported intermediate data.

## Functional exploration

Changing a control must update the result and its visual, not just a decorative label. For spatial explanations, label the axes, declare the coordinate convention and make faces or directions distinguishable. If comparing rotation order, compute both transform compositions; a text-only order switch does not demonstrate the geometry. Calculate an initial valid result. Use native labeled inputs and explicit units, reject non-finite/out-of-range values, and show useful empty/error states. Provide reset when exploration changes several values. Keep keyboard and touch alternatives to dragging. Put changing result text in an aria-live="polite" output.

Use stable control IDs or data-state-key. Standard inputs/outputs restore through the Host; use getState/setState for custom camera, selection or drawing state. Keep persisted state small. Do not persist animation frames, make automatic follow-up prompts, or load external resources. Host actions and approvals keep their existing meaning.

See the architecture-diagram, scenario-comparison, three-dimensional and geographic-overview templates for working examples. They are recipes, not new components or a promise of live data.

## Directed relationship layouts

Local `dagre` exposes `dagre.graphlib.Graph` and `dagre.layout(graph)` for directed graphs with shared dependencies and branches. Set actual node box sizes, then use returned node centers and edge points in SVG. It does not decide responsibilities or invent edges. Recompute direction from measured container width; wrap labels instead of shrinking fonts. The architecture template demonstrates the API, accessible selection and an HTML reading path before geometry initializes. A static SVG is appropriate when manual coordinates remain simple.
