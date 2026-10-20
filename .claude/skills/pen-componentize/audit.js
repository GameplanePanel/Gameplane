const MIN_TEXTS=3,COVER=0.75,PURITY=0.5,REPEAT_SCREENS=3,REPEAT_DESC=6,CAP=40;
const norm=s=>String(s).trim().toLowerCase();
const keep=v=>v.length>1&&!/^[\d\s.,:%/+×x·—–-]+$/.test(v);
const top=[];
Get(document,(n,c)=>{if(!c.parentCtx&&n.id!==document){top.push({id:n.id,name:n.name||"",t:n.type,r:!!n.reusable,w:c.bounds.width});c.skipChildren()}});
const comps={};
for(const t of top){if(t.r)comps[t.id]=t.name;else if(t.t==="frame"&&/Components$/.test(t.name))for(const k of (Get(t.id,{depth:1}).children||[]))if(k.reusable)comps[k.id]=k.name||k.id}
const textsOf=(n,acc)=>{if(n&&typeof n==="object"){if(n.type==="text"&&typeof n.content==="string"){const v=norm(n.content);if(keep(v))acc.add(v)}if(Array.isArray(n.children))n.children.forEach(k=>textsOf(k,acc))}return acc};
const CT={};
for(const id in comps){let t=null;try{t=Get(id,{depth:60,resolveInstances:true})}catch(e){try{t=Get(id,{depth:60})}catch(e2){}}if(!t)continue;const s=textsOf(t,new Set());if(s.size>=MIN_TEXTS)CT[id]=s}
const SLOT={};const isSlot=id=>{if(id in SLOT)return SLOT[id];let r=null;try{r=Get(id,{depth:1})}catch(e){}SLOT[id]=!r||r.slot!==undefined||!Array.isArray(r.children)||r.children.length===0;return SLOT[id]};
const GP={};for(const id in comps){const nm=comps[id]||"";if(/^Gameplane\//.test(nm)){const s=norm(nm.split("/").pop());if(s.includes(" "))GP[s]=id}}
const EXP=[
[n=>/^Screen\//.test(n)&&!/Login|Share Link|App Loading|Mobile|Dialog/.test(n),["kKFX9","gu5WY"]],
[n=>/Server Detail/.test(n)&&!/Mobile/.test(n),["S4k0x","I9kvlZ"]],
[n=>/Server Detail (—|·) Settings/.test(n),["EV8mp"]],
[n=>/^Screen\/Admin Settings|Standalone — Settings/.test(n),["Mp7Ep"]],
[n=>/Mobile/.test(n)&&!/Nav Drawer/.test(n),["fGbVF"]]];
const SIG={},R={dups:[],named:[],refKids:[],shell:[],ctl:[]};
const screens=top.filter(t=>!t.r&&t.t!=="note"&&!(t.t==="frame"&&/Components$/.test(t.name)));
const isCtl=n=>{const k=Array.isArray(n.children)?n.children:[];return k.length>=1&&k.length<=3&&k.every(c=>c.type==="text"||c.type==="icon")&&k.some(c=>c.type==="text")&&n.cornerRadius&&(n.fill||n.stroke)&&(n.padding!==undefined)};
for(const s of screens){
let tree;try{tree=Get(s.id,{depth:80})}catch(e){R.dups.push(s.id+" READ FAILED "+e);continue}
const ctx={id:s.id,refs:{},dups:[],named:[],refKids:[],btn:[],chip:[]};
const walk=(n,root)=>{const texts=new Set(),matched=new Set();let desc=0;
if(n.type==="ref"){ctx.refs[n.ref]=(ctx.refs[n.ref]||0)+1;if(Array.isArray(n.children)&&n.children.length&&!isSlot(n.ref))ctx.refKids.push(n.id+"→"+(comps[n.ref]||n.ref))}
if(n.type==="text"&&typeof n.content==="string"){const v=norm(n.content);if(keep(v))texts.add(v)}
const kids=Array.isArray(n.children)?n.children:[],res=[];
for(const k of kids){const r=walk(k,false);r.texts.forEach(t=>texts.add(t));r.matched.forEach(m=>matched.add(m));desc+=1+r.desc;res.push(r)}
const sig=n.type==="ref"?"R:"+n.ref+(kids.length?"("+res.map(r=>r.sig).join(",")+")":""):(n.type==="frame"||n.type==="group")?n.type[0]+"("+res.map(r=>r.sig).join(",")+")":n.type[0];
let rec=null;
if(n.type==="frame"&&!root){
if(texts.size>=MIN_TEXTS)for(const cid in CT){if(matched.has(cid))continue;const B=CT[cid];let i=0;B.forEach(t=>{if(texts.has(t))i++});if(i/B.size>=COVER&&i/texts.size>=PURITY){matched.add(cid);ctx.dups.push(n.id+" \""+n.name+"\" ≈ "+comps[cid]+" ("+cid+")")}}
const sn=norm(n.name||"");if(GP[sn]&&!matched.has(GP[sn]))ctx.named.push(n.id+" \""+n.name+"\"");
if(isCtl(n))(/chip|badge|pill|tag|status/i.test(n.name||"")?ctx.chip:ctx.btn).push(n.id);
if(desc>=REPEAT_DESC){const g=SIG[sig]=SIG[sig]||{screens:new Set(),recs:[],desc};g.screens.add(s.id);rec={sig,id:n.id,name:n.name,screen:s.id,parentSig:null};g.recs.push(rec)}}
for(const r of res)if(r.rec)r.rec.parentSig=sig;
return {texts,matched,desc,sig,rec}};
walk(tree,true);
const lbl=s.id+" "+s.name;
ctx.dups.forEach(d=>R.dups.push(lbl+" :: "+d));
ctx.named.forEach(d=>R.named.push(lbl+" :: "+d));
if(ctx.refKids.length)R.refKids.push(lbl+" :: "+ctx.refKids.length+" e.g. "+ctx.refKids.slice(0,4).join(", "));
const miss=[];for(const [f,ids] of EXP)if(f(s.name)&&s.t==="frame")for(const id of ids)if(!ctx.refs[id]&&!miss.includes(id))miss.push(id);
if(miss.length)R.shell.push(lbl+" :: missing "+miss.map(id=>(comps[id]||id)+" ("+id+")").join(", "));
if(ctx.btn.length+ctx.chip.length)R.ctl.push({n:ctx.btn.length+ctx.chip.length,line:lbl+" :: button-like "+ctx.btn.length+(ctx.btn.length?" e.g. "+ctx.btn.slice(0,3).join(","):"")+" | chip-like "+ctx.chip.length+(ctx.chip.length?" e.g. "+ctx.chip.slice(0,3).join(","):"")})}
const rep=Object.values(SIG).filter(g=>g.screens.size>=REPEAT_SCREENS).filter(g=>!g.recs.every(r=>r.parentSig&&SIG[r.parentSig]&&SIG[r.parentSig].screens.size>=REPEAT_SCREENS)).sort((a,b)=>b.screens.size*b.desc-a.screens.size*a.desc);
const sec=(title,arr)=>{Print("== "+title+" ["+arr.length+"]");arr.slice(0,CAP).forEach(l=>Print("  "+l));if(arr.length>CAP)Print("  … "+(arr.length-CAP)+" more")};
Print("CATALOG components="+Object.keys(comps).length+" textMatchable="+Object.keys(CT).length+" screensScanned="+screens.length);
sec("Detached copies of existing components (text match)",R.dups);
sec("Frames named like a Gameplane component but not instances",R.named);
sec("Instances whose content is replaced wholesale (ref with literal children)",R.refKids);
sec("Missing expected shell components",R.shell);
sec("Hand-built control-like frames (pill/button shapes not using Button/Chip components)",R.ctl.sort((a,b)=>b.n-a.n).map(c=>c.line));
sec("Repeated structures across screens (componentization candidates)",rep.map(g=>"screens="+g.screens.size+" nodes="+g.desc+" e.g. "+g.recs.slice(0,3).map(r=>r.id+" \""+r.name+"\"@"+r.screen).join(" ; ")))
