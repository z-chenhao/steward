import {useEffect,useRef} from 'react';
import {t} from '../../i18n';
import {ResultCard} from '../../components/ui/answer-controls';
import {AnswerSurface} from '../surface';
import {Icon} from '../icons';
import {useUIState} from '../state';
import {useInteractionGroup} from '../group';
import type {cardsSchema} from '../extensions';

// Frozen Runs/previews without the Cards service retain this exact renderer.
// Consumers include UI1.11 and earlier references and in-flight Runs. Remove
// only when those retained consumers have retired; managed failures never use it.
export function LegacyCardCarousel({props,reference}:{props:ReturnType<typeof cardsSchema.parse>;reference:string}){
 const group=useInteractionGroup(),list=useRef<HTMLDivElement>(null);const[state,update,saved]=useUIState(reference,{index:0},s=>({title:props.title,visible_item:props.items[s.index]?.title}));
 useEffect(()=>{const child=list.current?.children[state.index] as HTMLElement|undefined;if(child&&list.current)list.current.scrollTo({left:child.offsetLeft-list.current.offsetLeft,behavior:'instant'})},[saved.ready]);
 function move(delta:number){const index=Math.max(0,Math.min(props.items.length-1,state.index+delta));update({index});const child=list.current?.children[index] as HTMLElement|undefined;if(child&&list.current)list.current.scrollTo({left:child.offsetLeft-list.current.offsetLeft,behavior:'smooth'})}
 return <AnswerSurface title={props.title||props.items[0].title}><section className="native-carousel"><div className="native-carousel-heading">{props.title&&<h3 className="native-answer-title">{props.title}</h3>}<div className="native-carousel-controls"><button className="answer-icon-button" type="button" aria-label={t('Previous card')} disabled={state.index===0} onClick={()=>move(-1)}><Icon name="left"/></button><span>{state.index+1} / {props.items.length}</span><button className="answer-icon-button" type="button" aria-label={t('Next card')} disabled={state.index===props.items.length-1} onClick={()=>move(1)}><Icon name="right"/></button></div></div><div ref={list} className="native-carousel-list" onScroll={()=>{const children=[...list.current!.children] as HTMLElement[];const index=children.reduce((best,node,i)=>Math.abs(node.offsetLeft-list.current!.offsetLeft-list.current!.scrollLeft)<Math.abs(children[best].offsetLeft-list.current!.offsetLeft-list.current!.scrollLeft)?i:best,0);if(index!==state.index)update({index})}}>{props.items.map((item,i)=><ResultCard key={i} data-selected={group.selected===item.title}>{item.image_ref&&<img loading="lazy" src={'/ui/image?ref='+encodeURIComponent(item.image_ref)} alt=""/>}<button type="button" className="native-card-title" aria-pressed={group.selected===item.title} onClick={()=>group.select(item.title)}>{item.title}</button>{item.text&&<p>{item.text}</p>}{item.url&&<a href={item.url} target="_blank" rel="noopener noreferrer">{item.label||t('Open')}</a>}</ResultCard>)}</div></section>{saved.error&&<p role="alert" className="field-help">{t(saved.error)}</p>}</AnswerSurface>
}
