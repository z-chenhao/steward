// Private regression helper. Playwright 1.64.1's waitForFunction predicate
// treats a Promise as truthy, so asynchronous HTTP predicates must poll here.
// Reads only: never repair the selection/version after a recovery action.
export async function waitForUIState(page,ref,predicate){
  const deadline=Date.now()+5000;
  do{
    const state=await page.evaluate(async ref=>{
      const response=await fetch('/api/ui/state?'+new URLSearchParams({ref}),{cache:'no-store'});
      if(!response.ok)throw Error('Saved view unavailable.');
      return response.json();
    },ref);
    const matched=predicate(state);
    if(typeof matched!=='boolean')throw Error('UI state predicate must return a boolean.');
    if(matched)return state;
    await new Promise(resolve=>setTimeout(resolve,25));
  }while(Date.now()<deadline);
  throw Error('Original UI state did not satisfy the required predicate.');
}
