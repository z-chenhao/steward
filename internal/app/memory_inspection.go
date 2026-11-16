package app

import (
	"context"
	"github.com/z-chenhao/assistant/internal/memory"
)

func (a *App) InspectMemory(ctx context.Context, q memory.InspectionQuery) (memory.InspectionPage, error) {
	return a.runtimeRuns().InspectMemory(ctx, q, a.memory)
}

func (a *App) InspectMemoryPresented(ctx context.Context, q memory.InspectionQuery, reference string) (memory.InspectionPage, *memory.InspectionPresentation, error) {
	return a.runtimeRuns().InspectMemoryPresented(ctx, q, a.memory, reference)
}

func (a *App) MemoryInspectorStylesheet(ctx context.Context, reference string) (memory.InspectionStylesheet, error) {
	return a.runtimeRuns().MemoryInspectorStylesheet(ctx, reference)
}

func (a *App) MemoryInspectorProgram(ctx context.Context, reference string) (memory.InspectionProgram, error) {
	return a.runtimeRuns().MemoryInspectorProgram(ctx, reference)
}
