const $ = (id) => document.querySelector(`#${id}`);
const busy = (status) => ["queued", "gathering", "answering"].includes(status);
const pendingKey = "brokk-town-guide-pending";

// These handlers are the only network entry points. render and paintGuide only
// consume state; reconnecting SSE never resubmits or confirms a conversation.
export function guidePanel({api, getTown, getState, onOpen = () => {}}) {
  let townID = "", sending = false, pending = null, order = "", openedAt = 0;
  const rows = new Map(), conversations = new Map();
  try { pending = JSON.parse(sessionStorage.getItem(pendingKey) || "null"); } catch {}
  function savePending(value) { pending = value; try { value ? sessionStorage.setItem(pendingKey, JSON.stringify(value)) : sessionStorage.removeItem(pendingKey); } catch {} }
  function acknowledge(id, origin) {
    if (pending?.id !== id || pending?.town !== origin) return;
    const submitted = pending; savePending(null);
    if (origin === townID && $("guide-question").value.trim() === submitted.question) $("guide-question").value = "";
  }
  function selected() { return getState()?.towns?.[townID]; }
  async function command(body) {
    if (sending) return;
    sending = true; $("guide-error").textContent = ""; render();
    const origin = townID;
    try {
      const conversation = await api("/api/guide", {town: origin, ...body}, AbortSignal.timeout(30000));
      conversations.set(origin, conversation);
      if (origin === townID) renderConversation(conversation);
      if (body.action === "ask") acknowledge(body.id, origin);
    } catch (error) {
      const rejected = error.status >= 400 && error.status < 500 && error.status !== 408;
      if (rejected && body.action === "ask" && pending?.id === body.id) savePending(null);
      if (origin === townID) $("guide-error").textContent = rejected ? error.message : `${error.message} Check the conversation before trying again; the submission may have been saved.`;
    } finally { sending = false; render(); }
  }
  function row(turn) {
    if (rows.has(turn.id)) return rows.get(turn.id);
    const article = document.createElement("article"), question = document.createElement("p"), answer = document.createElement("p"), status = document.createElement("p"), proposal = document.createElement("p"), confirm = document.createElement("button");
    article.className = "guide-turn"; question.className = "guide-question"; answer.className = "guide-answer"; status.className = "muted";
    confirm.type = "button"; confirm.textContent = "Confirm this pause";
    article.append(question, answer, status, proposal, confirm);
    const item = {article, question, answer, status, proposal, confirm}; rows.set(turn.id, item); return item;
  }
  function renderConversation(conversation = {next:0, turns:[]}) {
    const cached = conversations.get(townID);
    if (cached && (cached.revision || 0) > (conversation.revision || 0)) conversation = cached;
    else conversations.set(townID, conversation);
    const turns = conversation.turns || [];
    if (pending?.town === townID && turns.some((turn) => turn.id === pending.id)) acknowledge(pending.id, townID);
    const ids = turns.map((turn) => turn.id).join(",");
    for (const turn of turns) {
      const item = row(turn);
      item.question.textContent = `You: ${turn.question}`; item.answer.textContent = `Town Guide: ${turn.answer || (busy(turn.status) ? "…" : "No completed answer.")}`;
      item.status.textContent = `${turn.status}${turn.detail ? ` · ${turn.detail}` : ""}`;
      item.proposal.textContent = turn.proposal ? `${turn.proposal.status === "confirmed" ? "Confirmed" : "Proposed action"}: ${turn.proposal.description}` : "";
      item.confirm.hidden = !turn.proposal || turn.proposal.status === "confirmed";
      item.confirm.disabled = sending;
      item.confirm.onclick = () => command({action:"confirm", id:turn.id, digest:turn.proposal.digest});
    }
    if (order !== ids) {
      $("guide-turns").replaceChildren(...turns.map((turn) => row(turn).article)); order = ids;
      for (const id of rows.keys()) if (!turns.some((turn) => turn.id === id)) rows.delete(id);
    }
    const active = turns.find((turn) => busy(turn.status));
    $("guide-status").textContent = active ? `Town Guide is ${active.status}.` : "Town Guide is ready. Answers describe saved observations.";
    $("guide-send").disabled = sending || !!active || !selected();
    $("guide-cancel").disabled = sending || !active;
    $("guide-cancel").onclick = () => active && command({action:"cancel",id:active.id});
    $("guide-retry").hidden = !(pending?.town === townID);
    $("guide-retry").disabled = sending;
  }
  function render() {
    $("ask-guide").disabled = !getTown();
    if (!$("guide-dialog").open) return;
    const town = selected();
    $("guide-town").textContent = townID;
    const profile = town?.config;
    $("guide-profile").textContent = profile ? `Uses town defaults: ${profile.harness || "configured harness"} · ${profile.model || "default model"} · ${profile.effort || "default effort"}.` : "This town is no longer available.";
    renderConversation(town?.guide);
  }
  $("guide-form").onsubmit = (event) => {
    event.preventDefault();
    const question = $("guide-question").value.trim(), town = selected();
    if (!question || !town || sending) return;
    if (pending?.town === townID) { $("guide-error").textContent = "Resolve the saved submission with Retry before asking a new question."; return; }
    const body = {town:townID,action:"ask",id:crypto.randomUUID(),sequence:conversations.get(townID)?.next ?? town.guide?.next ?? 0,question};
    savePending(body); void command(body);
  };
  $("guide-retry").onclick = () => { if (pending?.town === townID) return command(pending); };
  $("guide-refresh").onclick = () => command({action:"read"});
  $("guide-close").onclick = () => $("guide-dialog").close();
  $("ask-guide").onclick = () => {
    const town = getTown(); if (!town) return;
    if (townID !== town.id) { rows.clear(); order = ""; $("guide-turns").replaceChildren(); $("guide-question").value = ""; }
    townID = town.id; openedAt = performance.now();
    onOpen(); $("guide-dialog").showModal(); render(); $("guide-question").focus();
  };
  return {render, visual:() => ({open:$("guide-dialog").open, town:townID, openedAt})};
}

// A Guide is a person at Town Hall. It never uses the committed-delivery layer.
export function paintGuide(ctx, {town, now, motion, visual, positions}) {
  if (!town) return;
  const [x,y] = positions.hall, active = town.guide?.turns?.find((turn) => busy(turn.status));
  const opened = visual.open && visual.town === town.id;
  const wave = motion && opened && now - visual.openedAt < 1800;
  ctx.save();
  if (active || opened) {
    ctx.fillStyle = active ? "#ffd97780" : "#ffd97735";
    ctx.fillRect(x-27,y-25,17,22); ctx.fillRect(x+12,y-25,17,22);
  }
  if (active?.status === "gathering") {
    ctx.strokeStyle = "#f4d88699"; ctx.lineWidth = 2; ctx.setLineDash([3,7]);
    ctx.lineDashOffset = motion ? -now / 120 : 0;
    for (const role of active.houses || []) {
      if (!positions[role] || role === "hall") continue;
      const [hx,hy] = positions[role];ctx.beginPath();ctx.moveTo(hx,hy+60);ctx.lineTo(x,y+60);ctx.stroke();
    }
    ctx.setLineDash([]);
  }
  const gx=x+47+(wave?Math.sin((now-visual.openedAt)/400)*3:0), gy=y+75;
  ctx.fillStyle="#477d65";ctx.beginPath();ctx.moveTo(gx,gy-18);ctx.lineTo(gx-8,gy+5);ctx.lineTo(gx+8,gy+5);ctx.closePath();ctx.fill();
  ctx.fillStyle="#f2ceac";ctx.beginPath();ctx.arc(gx,gy-23,5,0,Math.PI*2);ctx.fill();
  ctx.strokeStyle="#f2ceac";ctx.lineWidth=3;ctx.beginPath();ctx.moveTo(gx+5,gy-13);ctx.lineTo(gx+12,gy-(wave?23+Math.sin(now/140)*4:10));ctx.stroke();
  ctx.fillStyle="#fff2c2";ctx.font="12px sans-serif";ctx.textAlign="center";
  ctx.fillText(active ? `Guide: ${active.status}` : "Town Guide",x,y+106);
  ctx.restore();
}
