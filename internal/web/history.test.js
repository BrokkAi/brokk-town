import test, { beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";
import { historyPanel } from "./history.js";
let fields;
function node() { return { children: [], listeners: {}, append(child) { this.children.push(child); }, replaceChildren(...children) { this.children = children; }, addEventListener(name, fn) { this.listeners[name] = fn; }, showModal() {}, close() { this.listeners.close(); } }; }
beforeEach(() => { fields = Object.fromEntries(["history-newest", "history-older", "history-items", "history-summary", "history-detail", "history-error", "history-status", "history-close", "history-dialog", "history-town", "town-history"].map((id) => [id, node()])); globalThis.document = { querySelector: (id) => fields[id.slice(1)], createElement: node }; });
afterEach(() => { delete globalThis.document; });
const tick = () => new Promise((resolve) => setImmediate(resolve));
const page = { total: 2, retention_days: 30, items: [{id:"issue:1",title:"<script>unsafe</script>",stage:"closed",updated:"2026-01-01T00:00:00Z"}], next: "cursor" };
test("history reads only on explicit navigation, replaces pages and keeps its town", async () => {
 const calls=[];let town="acme/first";
 historyPanel({getTown:()=>({id:town}),api:async(url,body)=>{calls.push({url,body});return body.task?{id:body.task,title:"Saved",stage:"closed",mayoral_decision:"declined"}:page;}});
 assert.equal(calls.length,0);fields["town-history"].onclick();await tick();town="acme/second";
 assert.equal(fields["history-items"].children[0].children[1].textContent,"<script>unsafe</script>");
 await fields["history-older"].onclick();assert.equal(fields["history-items"].children.length,1);
 assert.deepEqual(calls[1],{url:"/api/history",body:{town:"acme/first",after:"cursor",limit:50}});
 await fields["history-items"].children[0].children[4].children[0].onclick();
 assert.deepEqual(calls[2].body,{town:"acme/first",task:"issue:1"});assert.match(fields["history-detail"].textContent,/Mayor decision: declined/);
});
test("history cancels closed requests and ignores late responses and double clicks", async () => {
 let finish,signal,calls=0;
 historyPanel({getTown:()=>({id:"acme/project"}),api:async(_url,_body,s)=>{calls++;signal=s;return new Promise((resolve)=>{finish=resolve;});}});
 fields["town-history"].onclick();await fields["history-newest"].onclick();assert.equal(calls,1);
 fields["history-dialog"].close();assert.equal(signal.aborted,true);finish(page);await tick();assert.equal(fields["history-items"].children.length,0);
});
test("history failures remain visible and retry only on navigation", async () => {
 let calls=0;historyPanel({getTown:()=>({id:"acme/project"}),api:async()=>{calls++;throw new Error("Saved evidence missing");}});
 fields["town-history"].onclick();await tick();assert.match(fields["history-error"].textContent,/Saved evidence missing/);assert.equal(calls,1);
 await fields["history-newest"].onclick();assert.equal(calls,2);
});
