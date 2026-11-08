import {Menu} from '@base-ui/react/menu';
import {t} from '../../i18n';
import {useLayoutEffect,useRef,useState,type ReactNode} from 'react';
import {Icon} from '../../intelligent-ui/icons';
import {buttonVariants} from './styles';
import {usePopupZoom} from './popup-zoom';
export type MenuAction={label:string;run:()=>void|Promise<void>;icon?:ReactNode;disabled?:boolean};
function MenuFrame({label,icon,iconOnly=false,align='end',children}:{label:string;icon?:ReactNode;iconOnly?:boolean;align?:'start'|'end';children:ReactNode}){
 const trigger=useRef<HTMLButtonElement>(null),[container,setContainer]=useState<HTMLElement>();
 const positionerStyle=usePopupZoom();
 useLayoutEffect(()=>setContainer(trigger.current?.closest('dialog')??undefined),[]);
 return <Menu.Root><Menu.Trigger ref={trigger} className={iconOnly?'answer-icon-button':buttonVariants({variant:'secondary'})+' control-toolbar-button'} aria-label={t(label)} title={t(label)}>{iconOnly?<Icon name="more"/>:<>{icon}{t(label)}</>}</Menu.Trigger>
  <Menu.Portal container={container}><Menu.Positioner className="control-select-positioner" style={positionerStyle} positionMethod="fixed" align={align} sideOffset={5} collisionPadding={8}>
   <Menu.Popup className="shared-action-menu">{children}</Menu.Popup>
  </Menu.Positioner></Menu.Portal>
 </Menu.Root>;
}
export function ActionMenu({actions,label='Options'}:{actions:MenuAction[];label?:string}){
 return <MenuFrame label={label} iconOnly>{actions.map((action,index)=><Menu.Item key={index} disabled={action.disabled} onClick={()=>void action.run()}>{action.icon}{t(action.label)}</Menu.Item>)}</MenuFrame>;
}
export function FilterMenu({value,options,checked,checkboxLabel,onValueChange,onCheckedChange}:{value:string;options:{value:string;label:string}[];checked:boolean;checkboxLabel:string;onValueChange:(value:string)=>void;onCheckedChange:(checked:boolean)=>void}){
 return <MenuFrame label="Filters" icon={<Icon name="filter"/>} align="start"><Menu.RadioGroup aria-label={t('Status')} value={value} onValueChange={onValueChange}>
 {options.map(option=><Menu.RadioItem key={option.value} value={option.value} closeOnClick><span className="control-menu-check"><Menu.RadioItemIndicator><Icon name="check"/></Menu.RadioItemIndicator></span>{t(option.label)}</Menu.RadioItem>)}
 </Menu.RadioGroup><Menu.Separator className="control-menu-separator"/><Menu.CheckboxItem checked={checked} onCheckedChange={onCheckedChange} closeOnClick><span className="control-menu-check"><Menu.CheckboxItemIndicator><Icon name="check"/></Menu.CheckboxItemIndicator></span>{t(checkboxLabel)}</Menu.CheckboxItem></MenuFrame>;
}
