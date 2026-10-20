const ROWLBL=/^Row (\d\d) · /,GAP_X=200,GAP_ROW=400;
const top=[];
Get(document,(n,c)=>{if(!c.parentCtx&&n.id!==document){top.push({id:n.id,name:n.name||"",t:n.type,r:!!n.reusable,x:c.bounds.x,y:c.bounds.y,w:c.bounds.width,h:c.bounds.height});c.skipChildren()}});
const boards=top.filter(n=>n.t==="frame"&&/Components$/.test(n.name));
const labels=top.filter(n=>n.t==="note"&&ROWLBL.test(n.name)).sort((a,b)=>a.y-b.y);
const issues=[];
top.filter(n=>n.r).forEach(n=>issues.push("LOOSE COMPONENT at root: "+n.id+" "+n.name));
let comps=0;
for(const b of boards){for(const k of (Get(b.id,{depth:1}).children||[])){if(k.reusable)comps++;else if(k.type!=="text"&&!/^HeroUI/.test(b.name))issues.push("NON-COMPONENT on "+b.name+": "+k.id+" "+(k.name||""))}}
for(let i=0;i<top.length;i++)for(let j=i+1;j<top.length;j++){const a=top[i],b=top[j];if(a.x<b.x+b.w&&b.x<a.x+a.w&&a.y<b.y+b.h&&b.y<a.y+a.h)issues.push("OVERLAP "+a.id+" "+a.name+" <> "+b.id+" "+b.name)}
const bandBottom=Math.max(0,...boards.map(b=>b.y+b.h));
const rows=labels.map((l,i)=>({l,top:l.y,bottom:i+1<labels.length?labels[i+1].y:Infinity,m:[]}));
for(const n of top){if(boards.includes(n)||labels.includes(n))continue;if(n.y<bandBottom){if(n.t!=="note")issues.push("IN COMPONENT BAND but not a board: "+n.id+" "+n.name);continue}const r=rows.find(r=>n.y>=r.top&&n.y<r.bottom);if(!r){issues.push("OUTSIDE ROWS: "+n.id+" "+n.name);continue}r.m.push(n)}
if(labels.length&&labels[0].y<bandBottom+GAP_ROW)issues.push("FIRST ROW overlaps the component band");
rows.forEach((r,i)=>{const k=+ROWLBL.exec(r.l.name)[1];if(k!==i+1)issues.push("ROW NUMBER "+r.l.name+" expected "+String(i+1).padStart(2,"0"));
const s=r.m.filter(n=>n.t!=="note").sort((a,b)=>a.x-b.x);if(!s.length)issues.push("EMPTY ROW "+r.l.name);
let x=0;for(const n of s){if(Math.abs(n.y-r.top)>1)issues.push("ROW "+r.l.name+": not top-aligned "+n.id+" "+n.name);if(Math.abs(n.x-x)>1)issues.push("ROW "+r.l.name+": x="+Math.round(n.x)+" expected "+Math.round(x)+" at "+n.id+" "+n.name);x=n.x+n.w+GAP_X}
const bottom=Math.max(r.top,...r.m.map(n=>n.y+n.h));if(r.bottom!==Infinity&&r.bottom-bottom<GAP_ROW-1)issues.push("ROW "+r.l.name+": next row starts "+Math.round(r.bottom-bottom)+"px below its tallest item, expected >= "+GAP_ROW)});
Print("root nodes",top.length,"| boards",boards.map(b=>b.id+" "+b.name).join(", "),"| components on boards",comps,"| rows",rows.length,"| screens in rows",rows.reduce((a,r)=>a+r.m.filter(n=>n.t!=="note").length,0));
rows.forEach(r=>Print(r.l.name,"→",r.m.filter(n=>n.t!=="note").sort((a,b)=>a.x-b.x).map(n=>n.id).join(" ")));
Print(issues.length?"ISSUES ("+issues.length+"):":"NO ISSUES");issues.slice(0,120).forEach(i=>Print(" - "+i))
