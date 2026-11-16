import {connectTraceBrowser,type TraceBrowser} from './trace-browser';
// Each frozen page/workbench shares one Worker across its owning controls.
export type TraceProgramEntry={active:boolean;browser:Promise<TraceBrowser>};
const programs=new WeakMap<HTMLElement,TraceProgramEntry>();
export function traceProgram(root:HTMLElement):TraceProgramEntry {
 let entry=programs.get(root);
 if(!entry){entry={active:true,browser:connectTraceBrowser(root.dataset.traceProgramRevision??'')};programs.set(root,entry)}
 return entry;
}
export function disposeTraceProgram(root:HTMLElement){
 const entry=programs.get(root);if(!entry)return;
 entry.active=false;programs.delete(root);void entry.browser.then(browser=>browser.close()).catch(()=>{});
}
