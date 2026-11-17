package computeruse

const coreDocumentation = `type ElementRef = { observationRevision: string; index: number };
type Point = [number, number];
interface Surface {
 getAXState(options?: {full?: boolean}): Promise<AXObservation>;
 getScreenshot(): Promise<ScreenshotObservation>;
 getAXStateAndScreenshot(options?: {full?: boolean}): Promise<{ax: AXObservation; screenshot: ScreenshotObservation}>;
 click(target: number | ElementRef | Point, options?: {button?: "left" | "right"; count?: 1 | 2}): Promise<unknown>;
 drag(from: Point, to: Point): Promise<unknown>;
 scroll(target: number | ElementRef | Point, direction: "up" | "down" | "left" | "right", amount?: number): Promise<unknown>;
 setValue(target: number | ElementRef, value: string): Promise<unknown>;
 selectText(target: number | ElementRef, text: string): Promise<unknown>;
 performSecondaryAction(target: number | ElementRef, action: string): Promise<unknown>;
 waitForChange(options?: {sinceRevision?: string; timeoutMs?: number; full?: boolean}): Promise<AXObservation>;
 documentation(): Promise<string>;
}
interface AXObservation {surfaceId: string; revision: string; baseRevision?: string; kind: "full" | "diff"; state: string}
interface ScreenshotObservation {surfaceId: string; revision: string; width: number; height: number; image: {artifactRef: string}}
interface App extends Surface {paste(text: string): Promise<unknown>; typeText(text: string): Promise<unknown>; pressKey(key: string): Promise<unknown>}
interface Tab extends Surface {paste(target: number | ElementRef | null, text: string): Promise<unknown>; typeText(target: number | ElementRef | null, text: string): Promise<unknown>; pressKey(target: number | ElementRef | null, key: string): Promise<unknown>; goto(url: string): Promise<unknown>; back(): Promise<unknown>; forward(): Promise<unknown>; reload(): Promise<unknown>; close(): Promise<unknown>}
Every input invalidates old element and screenshot refs. Observe after actions. Diff state is empty when unchanged, otherwise @@ zeroBasedLine,removedLines,addedLines @@ followed by - removed and + added lines. Screenshots have independent revisions; image(screenshot.image) selects its Artifact for model output. No binary enters JavaScript. Coordinates require the latest screenshot and unchanged geometry. WAIT_TIMEOUT.details.observation contains the last bounded summary. Handles are invocation and lease scoped; store only stable refs. All input, navigation, launch/create and transfers require exact action approval in this conservative policy. Approval never transfers to upload, overwrite or export. Secondary actions must appear in the latest AX tree. Native methods depend on Driver support and fail closed when unavailable.`
const browserDocumentation = `interface Playwright {
 documentation(): Promise<string>;
 locator(selector: string): {
  count(): Promise<number>;
  textContent(): Promise<string | null>;
  click(): Promise<unknown>;
  fill(value: string): Promise<unknown>;
  waitFor(options?: {timeoutMs?: number}): Promise<unknown>;
  download(): Promise<{computerId: string; path: string}>;
  upload(file: {computerId: string; path: string}): Promise<unknown>;
 };
 waitForURL(url: string, options?: {timeoutMs?: number}): Promise<unknown>;
}
Call capabilities.list() before get("playwright"). Navigation invalidates the capability, including same-URL reload. Every method invokes ToolExecutor. CSS selectors only; no evaluate, arbitrary JavaScript, filesystem, credentials or network API. Downloads use the controlled downloads directory. Upload waits for the chooser and requires separate frozen upload approval; a file import is not upload consent. Import files only using cua.files.importArtifact and export registered downloads with cua.files.exportArtifact. DOM operations may send data and require approval. Waits are bounded to 10 seconds.`
