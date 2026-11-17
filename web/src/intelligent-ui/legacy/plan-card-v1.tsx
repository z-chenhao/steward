// Adapted from CopilotKit/OpenIntelligentUI @ 8391a94c1c3939c679e40ddd7bfc08a9ef690e70.
// MIT; see web/licenses/OpenIntelligentUI.txt. Native rendering; no generated markup.
"use client";

import { useEffect, useRef } from "react";

// Only pre-Plan-service frozen Runs and previews use this renderer.
// Remove after their published references and in-flight Runs are no longer retained.
interface PlanCardProps {
  status: "executing" | "inProgress" | "complete";
  approach?: string;
  technology?: string;
  keyElements?: string[];
}

export function LegacyPlanCard({ status, approach, technology, keyElements }: PlanCardProps) {
  const detailsRef = useRef<HTMLDetailsElement>(null);
  const isRunning = status === "executing" || status === "inProgress";

  useEffect(() => {
    if (!detailsRef.current) return;
    detailsRef.current.open = isRunning;
  }, [isRunning]);

  return (
    <details ref={detailsRef} className="native-plan" open>
      <summary><span>{isRunning ? "Planning visualization…" : `Plan: ${technology || "visualization"}`}</span><svg viewBox="0 0 24 24" aria-hidden="true"><path d="m9 5 7 7-7 7"/></svg></summary>
      {approach && <div className="native-plan-body"><p>{approach}</p>{keyElements&&keyElements.length>0&&<ul>{keyElements.map((element,i)=><li key={i}>{element}</li>)}</ul>}</div>}
    </details>
  );
}
