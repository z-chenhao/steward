# OpenIntelligentUI answer design

Use one visual focal point. Explain the conclusion in the normal reply. Source data, units, assumptions and illustrative values must remain clear.

Use Table, barChart, pieChart and scheduleTime when they fit the question. Do not substitute a table for a mechanism or a single series for a multi-option scenario. plan_visualization is optional planning, not the delivered answer. Custom HTML/SVG follows generateSandboxedUi's ordered CSS/markup/functions/expressions contract. Local d3 and THREE are available; read references/visualization.md for their boundaries. Keep styles widget-specific: upstream theme variables, SVG helpers and form controls are already injected.

SVG provides `.c-purple`, `.c-teal`, `.c-coral`, `.c-pink`, `.c-gray`, `.c-blue`, `.c-green`, `.c-amber`, `.c-red` and `.box`, `.node`, `.arr`, `.leader`, `.t`, `.ts`, `.th`. Measure the container and reserve room for labels. Reflow a desktop graph on narrow screens; a responsive viewBox alone does not keep text readable. Simplify crowded graphs rather than shrinking text or hiding content.

Calculators need labels, units, bounded finite inputs, an initial useful result and a reset control. Reject blank inputs and never display NaN/Infinity. Teaching tools and simulations should expose only meaningful controls, show how an input changes an outcome, and support pause/reset where time matters. Respect reduced motion.

Use compact flex/grid layouts with one spacing rhythm. Do not duplicate the Host title, add a landing-page hero, nest decorative cards or build app navigation inside the answer. Support both themes through tokens, keyboard controls, visible focus, and labels in addition to colors.

Follow-ups use Websandbox.connection.remote.sendPrompt({text}); links use its openLink({url}). The Host's user-confirmed bridge preserves ordinary action authorization. Local slider state stays local; a follow-up explicitly names selected values.

Steward supplies the native typography and control skin. Group adjacent actions in the provided `.actions`, `.controls` or `.button-group` flex wrapper; these wrap with a consistent gap. Do not add decorative outer cards, oversized headings, emoji ornaments, control scaling or a text-version toggle. The Host shows the text alternative on failure or without scripting.


Steward extensions use the same compact styling. Sorting and filtering, graphic/data exports and display modes belong in the Host options menu. Use a real field label for each input and content titles where needed; do not put instructions, feature descriptions or technical implementation notes on the page. Keep errors factual and actionable. Keep source citations, data units and explicitly illustrative assumptions with the actual content.

Stateful custom controls use stable IDs or `data-state-key`. The Host restores standard controls and outputs; preserve custom drawing state through getState/setState, and expose a small factual model context. `callTool({action})` only proposes an exact declared action; the Host button owns user intent. Never imitate an approval or automatically call actions during rendering. Keep native form fields and card content in their registered components rather than generating duplicate HTML versions.
