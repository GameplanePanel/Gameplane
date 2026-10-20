const kids=n=>Array.isArray(n.children)?n.children:[];
const P=v=>JSON.stringify(v);
const find=(root,id)=>{let r=null;const w=(n,p,i)=>{if(r)return;if(n.id===id){r={n,p,i};return}kids(n).forEach((k,j)=>w(k,n,j))};w(root,null,0);return r};
const strip=n=>{const o={...n};delete o.id;if(Array.isArray(o.children))o.children=o.children.map(strip);return o};
const roots=()=>{const t=[];Get(document,(n,c)=>{if(!c.parentCtx&&n.id!==document){t.push({id:n.id,name:n.name||"",t:n.type,r:!!n.reusable,x:c.bounds.x,y:c.bounds.y,w:c.bounds.width,h:c.bounds.height});c.skipChildren()}});return t};
const boardId=()=>{const b=roots().find(n=>n.name==="Gameplane: App Components");if(!b)throw new Error("no Gameplane board");return b.id};
const screenIds=()=>roots().filter(t=>!t.r&&t.t!=="note"&&!(t.t==="frame"&&/Components$/.test(t.name))).map(t=>t.id);
const boardBottom=b=>{let m=0;Get(b,(n,c)=>{if(!c.parentCtx)return;if(!c.parentCtx.parentCtx){m=Math.max(m,c.bounds.y+c.bounds.height);c.skipChildren()}});return m};
const parkOnBoard=(b,id)=>{const y=boardBottom(b)+80;Update(id,{x:160,y});let h=0;Get(b,(n,c)=>{if(!c.parentCtx)return;if(!c.parentCtx.parentCtx){if(n.id===id)h=c.bounds.height;c.skipChildren()}});const B=Get(b,{depth:0});if(y+h+160>B.height)Update(b,{height:y+h+160})};
const sig=n=>n.type==="ref"?"R:"+n.ref+(kids(n).length?"("+kids(n).map(sig).join(",")+")":""):(n.type==="frame"||n.type==="group")?n.type[0]+"("+kids(n).map(sig).join(",")+")":n.type[0];
// Fill in, then run. PROMO = {name: "Gameplane/<Name>", source: "<id of one clean occurrence>",
//   dryRun: true (list occurrences only, change nothing), exclude: ["<occurrence id>", ...] (optional)}
const PROMO={name:"Gameplane/<Name>",source:"<id>",dryRun:true,exclude:[]};
const SKIPK=new Set(["id","children","type","ref","descendants","reusable","context","x","y"]);
const NEUTRAL={fill:"#00000000",stroke:"#00000000",strokeWidth:0,cornerRadius:0,padding:0,gap:0,opacity:1,enabled:true,clip:false,rotation:0,layout:"horizontal",justifyContent:"start",alignItems:"start",textGrowth:"auto"};
const neutral=(k,cv,node)=>{if(k==="effect"){const a=Array.isArray(cv)?cv:[cv];const v=a.map(e=>({...e,enabled:false}));return Array.isArray(cv)?v:v[0]}if((k==="width"||k==="height")&&node.type==="frame")return "fit_content";if(k in NEUTRAL)return NEUTRAL[k];return undefined};
const overrides=(C,O)=>{const out={root:{},desc:{}};
const rec=(c,o,isRoot)=>{if(c.type!==o.type)return "type "+c.type+"≠"+o.type+" at "+c.id;if(c.type==="ref"&&c.ref!==o.ref)return "ref differs at "+c.id;
const ks=new Set([...Object.keys(c),...Object.keys(o)]);for(const k of ks){if(SKIPK.has(k))continue;if(P(c[k])===P(o[k]))continue;let v=o[k];if(v===undefined){v=neutral(k,c[k],c);if(v===undefined)return "cannot unset "+k+" at "+c.id}if(isRoot)out.root[k]=v;else(out.desc[c.id]=out.desc[c.id]||{})[k]=v}
if(c.type==="ref"){const dc=c.descendants||{},dd=o.descendants||{};for(const k of new Set([...Object.keys(dc),...Object.keys(dd)])){if(P(dc[k])===P(dd[k]))continue;if(dd[k]===undefined)return "nested override "+k+" only in component at "+c.id;out.desc[c.id+"/"+k]=dd[k]}
if(kids(c).length||kids(o).length){if(P(kids(c).map(strip))!==P(kids(o).map(strip)))return "slot content differs at "+c.id}return null}
const kc=kids(c),ko=kids(o);if(kc.length!==ko.length)return "child count at "+c.id;for(let i=0;i<kc.length;i++){const e=rec(kc[i],ko[i],false);if(e)return e}return null};
const e=rec(C,O,true);return e?{error:e}:out};
const canon=v=>Array.isArray(v)?v.map(canon):v&&typeof v==="object"?Object.fromEntries(Object.keys(v).sort().filter(k=>!["id","ref","descendants","reusable","context"].includes(k)).map(k=>[k,canon(v[k])])):v;
const firstDiff=(a,b,p)=>{if(P(a)===P(b))return null;if(a&&b&&typeof a==="object"&&typeof b==="object"){for(const k of new Set([...Object.keys(a),...Object.keys(b)])){const d=firstDiff(a[k],b[k],p+"."+k);if(d)return d}}return p+": "+P(a).slice(0,60)+" vs "+P(b).slice(0,60)};
const view=id=>{const r=canon(Get(id,{depth:80,resolveInstances:true}));delete r.x;delete r.y;delete r.name;return r};
const b=boardId();const all=screenIds();
let src=null;const occ=[];
for(const s of all){const t=Get(s,{depth:80});const w=(n,p,i)=>{if(n.id===PROMO.source)src={n,p,i,s};kids(n).forEach((k,j)=>w(k,n,j))};w(t,null,0)}
if(!src)throw new Error("source "+PROMO.source+" not found");
const SG=sig(src.n);
for(const s of all){const t=Get(s,{depth:80});const w=(n,p,i)=>{if(n.type==="frame"&&p&&sig(n)===SG){if(!(PROMO.exclude||[]).includes(n.id))occ.push({n,p,i,s});return}kids(n).forEach((k,j)=>w(k,n,j))};w(t,null,0)}
const txt=n=>{const a=[];const w=x=>{if(x.type==="text"&&typeof x.content==="string")a.push(x.content);kids(x).forEach(w)};w(n);return a.slice(0,3).join(" / ")};
if(PROMO.dryRun)occ.forEach(o=>Print("OCC "+o.s+" "+o.n.id+" '"+(o.n.name||"")+"' :: "+txt(o.n)));
if(!PROMO.dryRun){
const comp=Copy(PROMO.source,b,{name:PROMO.name});Update(comp,{reusable:true});parkOnBoard(b,comp);
const C=Get(comp,{depth:80});let ok=0;const rep=[];
for(const o of occ){if(o.p.type==="ref"){rep.push(o.s+":"+o.n.id+" parent is instance");continue}
const ov=overrides(C,o.n);if(ov.error){rep.push(o.s+":"+o.n.id+" "+ov.error);continue}
const node={type:"ref",ref:comp,...ov.root,descendants:ov.desc};if(o.n.name)node.name=o.n.name;if(o.p.layout==="none"){node.x=o.n.x||0;node.y=o.n.y||0}
const before=view(o.n.id);const nid=Insert(o.p.id,node);Move(nid,o.p.id,o.i);const after=view(nid);
const d=firstDiff(before,after,"");if(d){Delete(nid);rep.push(o.s+":"+o.n.id+" VERIFY "+d);continue}
Delete(o.n.id);ok++;Print("RESULT "+o.s+" "+o.n.id+" → "+nid)}
if(!ok){Delete(comp);Print("PROMO "+PROMO.name+" ABANDONED (no occurrence verified); "+rep.join(" | "))}
else Print("PROMO "+PROMO.name+" component "+comp+" replaced "+ok+"/"+occ.length+(rep.length?"; kept: "+rep.join(" | "):""))
}
