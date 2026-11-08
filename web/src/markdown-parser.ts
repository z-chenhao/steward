import {Marked,Renderer} from 'marked';
import markedCjkFriendly from 'marked-cjk-friendly';
import remend from 'remend';

const escape=(text:string)=>text.replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]!));
export function markdownLink(href:string,origin='http://steward.invalid'):string|null{
 if(/^artifact:[A-Za-z0-9_-]{1,128}$/.test(href))return '/artifact?ref='+encodeURIComponent(href);
 try{const url=new URL(href,origin);return ['http:','https:'].includes(url.protocol)?url.href:null}catch{return null}
}
export function parseMarkdown(text:string,streaming=false,origin='http://steward.invalid'):string {
const renderer=new Renderer();
renderer.html=({text})=>escape(text);
renderer.image=({text})=>escape(text);
renderer.link=function({href,title,tokens}){
 const content=this.parser.parseInline(tokens),target=markdownLink(href,origin);
 return target?`<a href="${escape(target)}"${title?` title="${escape(title)}"`:''} rel="noopener noreferrer">${content}</a>`:content;
};
const markdown=new Marked({renderer,gfm:true,breaks:true},markedCjkFriendly());
return markdown.parse(streaming?remend(text,{links:false,images:false}):text,{async:false});
}
