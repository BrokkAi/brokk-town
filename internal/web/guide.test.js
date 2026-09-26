import test, {beforeEach, afterEach} from "node:test";
import assert from "node:assert/strict";
import {guidePanel, paintGuide} from "./guide.js";
let fields, saved, state;
function node() {return {children:[],listeners:{},value:"",open:false,textContent:"",append(...children){this.children.push(...children);},replaceChildren(...children){this.children=children;},showModal(){this.open=true;},close(){this.open=false;},focus(){},addEventListener(name,fn){this.listeners[name]=fn;}};}
const tick=()=>new Promise((resolve)=>setImmediate(resolve));
beforeEach(()=>{
 fields=Object.fromEntries(["ask-guide","guide-dialog","guide-error","guide-question","guide-town","guide-profile","guide-turns","guide-status","guide-send","guide-cancel","guide-retry","guide-form","guide-refresh","guide-close"].map(id=>[id,node()]));
 globalThis.document={querySelector:(id)=>fields[id.slice(1)],createElement:node};saved=new Map();globalThis.sessionStorage={getItem:(k)=>saved.get(k),setItem:(k,v)=>saved.set(k,v),removeItem:(k)=>saved.delete(k)};
 state={towns:{"acme/project":{id:"acme/project",config:{harness:"fixture",model:"model"},guide:{revision:0,next:0,turns:[]}}}};
});
afterEach(()=>{delete globalThis.document;delete globalThis.sessionStorage;});
function panel(api){return guidePanel({api,getTown:()=>state.towns["acme/project"],getState:()=>state});}
function submit(text){fields["guide-question"].value=text;fields["guide-form"].onsubmit({preventDefault(){}});}
const proposal={action:"pause",role:"issue",digest:"exact",status:"proposed",description:"Pause issue in acme/project after current work finishes."};
test("Guide rendering and paint never submit; confirmation needs a button click and retains focus identity",async()=>{
 const calls=[];const ui=panel(async(url,body)=>{calls.push({url,body});return state.towns["acme/project"].guide;});
 fields["ask-guide"].onclick();assert.equal(calls.length,0);
 state.towns["acme/project"].guide={revision:2,next:1,turns:[{id:"question-one",question:"<script>pause</script>",answer:"<img> saved answer",status:"complete",proposal}]};
 ui.render();const article=fields["guide-turns"].children[0],confirm=article.children[4];
 assert.equal(article.children[0].textContent,"You: <script>pause</script>");assert.match(article.children[1].textContent,/<img>/);
 for(let i=0;i<20;i++)ui.render();assert.equal(fields["guide-turns"].children[0].children[4],confirm);assert.equal(calls.length,0);
 const drawing=[];const ctx=new Proxy({}, {get:(_target,key)=>(...args)=>drawing.push([key,...args]),set:()=>true});
 paintGuide(ctx,{town:state.towns["acme/project"],now:100,motion:false,visual:ui.visual(),positions:{hall:[100,100],issue:[200,200]}});
 assert.equal(calls.length,0);assert.ok(drawing.some(([key,...args])=>key==="fillText"&&args[0]==="Town Guide"));
 await confirm.onclick();assert.deepEqual(calls[0],{url:"/api/guide",body:{town:"acme/project",action:"confirm",id:"question-one",digest:"exact"}});
});
test("Guide reconnect retains an uncertain submission and retries the same identity only explicitly",async()=>{
 const calls=[];let fail=true;
 const api=async(_url,body)=>{calls.push({...body});if(fail)throw new Error("Connection lost");return {revision:1,next:1,turns:[{id:body.id,question:body.question,status:"queued",answer:""}]};};
 panel(api);fields["ask-guide"].onclick();submit("Explain failures");await tick();
 assert.equal(calls.length,1);const original=calls[0];assert.match(fields["guide-error"].textContent,/may have been saved/);
 const reconnected=panel(api);fields["ask-guide"].onclick();reconnected.render();assert.equal(calls.length,1);assert.equal(fields["guide-retry"].hidden,false);
 fail=false;await fields["guide-retry"].onclick();assert.deepEqual(calls[1],original);assert.equal(saved.size,0);
 // An older SSE frame must not replace the acknowledged queue state.
 reconnected.render();assert.match(fields["guide-status"].textContent,/queued/);assert.equal(fields["guide-send"].disabled,true);
});
test("Guide rejects invalid submissions without trapping the draft and cancellation is explicit",async()=>{
 const calls=[];let reject=true;
 const ui=panel(async(_url,body)=>{calls.push(body);if(reject){const error=new Error("Question too long");error.status=400;throw error;}return {revision:3,next:1,turns:[{id:"question-one",question:"x",status:"cancelled",answer:""}]};});
 fields["ask-guide"].onclick();submit("x");await tick();assert.equal(saved.size,0);assert.equal(fields["guide-question"].value,"x");assert.equal(fields["guide-error"].textContent,"Question too long");
 state.towns["acme/project"].guide={revision:2,next:1,turns:[{id:"question-one",question:"x",answer:"Answering",status:"answering"}]};ui.render();reject=false;
 fields["guide-close"].onclick();assert.equal(calls.length,1);fields["ask-guide"].onclick();await fields["guide-cancel"].onclick();assert.equal(calls[1].action,"cancel");assert.equal(calls[1].id,"question-one");
});
test("Guide uses static context paths under reduced motion and no delivery layer",()=>{
 const strokes=[];const ctx=new Proxy({}, {get:(_target,key)=>(...args)=>strokes.push([key,...args]),set:(target,key,value)=>{target[key]=value;strokes.push([key,value]);return true;}});
 const town=state.towns["acme/project"];town.guide.turns=[{status:"gathering",houses:["issue"]}];
 paintGuide(ctx,{town,now:3000,motion:false,visual:{open:true,town:town.id,openedAt:2500},positions:{hall:[100,100],issue:[200,200]}});
 assert.ok(strokes.some(([key,value])=>key==="lineDashOffset"&&value===0));assert.ok(strokes.some(([key])=>key==="lineTo"));assert.ok(strokes.some(([key,text])=>key==="fillText"&&text==="Guide: gathering"));
});
