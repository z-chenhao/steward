// Keep native details as the single panel state; the toolbar is its enhanced trigger.
export function enhanceFilterRails(){
 for(const trigger of document.querySelectorAll<HTMLButtonElement>('[data-list-filter-toggle]')){
  const rail=document.getElementById(trigger.dataset.listFilterToggle??'') as HTMLDetailsElement|null;
  const summary=rail?.querySelector<HTMLElement>(':scope > summary');
  if(!rail||!summary)continue;
  trigger.setAttribute('aria-controls',rail.id);
  const sync=()=>trigger.setAttribute('aria-expanded',String(rail.open));
  trigger.addEventListener('click',()=>{rail.open=!rail.open;sync()});
  rail.addEventListener('toggle',sync);sync();
  summary.hidden=true;trigger.hidden=false;
 }
}
