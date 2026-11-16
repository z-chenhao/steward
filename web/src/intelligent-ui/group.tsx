import {createContext,useContext,type ReactNode} from 'react';
import {getUIState,useUIState} from './state';
export type ToolAction={reference:string;id:string;label:string;tool:string;arguments:Record<string,unknown>};
type Group={query:string;selected:string;setQuery:(q:string)=>void;select:(label:string)=>void;actions:ToolAction[];readState:()=>Promise<{query:string;selected:string}>};
const context=createContext<Group>({query:'',selected:'',setQuery:()=>{},select:()=>{},actions:[],readState:async()=>({query:'',selected:''})});
export function InteractionGroup({reference,actions,children}:{reference:string;actions:ToolAction[];children:ReactNode}){
 const [state,update]=useUIState(reference,{query:'',selected:''},s=>({filter:s.query,selection:s.selected}));
 const readState=async()=>{
  const view=await getUIState(reference);
  if(view.error||(view.state.query!==undefined&&typeof view.state.query!=='string')||(view.state.selected!==undefined&&typeof view.state.selected!=='string'))throw Error('Saved view unavailable.');
  return {query:typeof view.state.query==='string'?view.state.query:'',selected:typeof view.state.selected==='string'?view.state.selected:''};
 };
 return <context.Provider value={{readState,query:state.query,selected:state.selected,setQuery:query=>update({query}),select:selected=>update({selected:state.selected===selected?'':selected}),actions}}>{children}</context.Provider>
}
export const useInteractionGroup=()=>useContext(context);
