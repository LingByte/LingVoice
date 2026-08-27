package admin

// adminHTML is the embedded admin panel HTML page.
// It's a single-page app with no external dependencies.
const adminHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>LingVoice Admin</title>
<style>
* { margin: 0; padding: 0; box-sizing: border-box; }
body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; background: #1a1a2e; color: #e0e0e0; }
.navbar { background: #16213e; padding: 12px 24px; display: flex; align-items: center; gap: 16px; }
.navbar h1 { font-size: 20px; color: #0f3460; }
.navbar .logo { font-size: 24px; }
.navbar .status { margin-left: auto; font-size: 13px; color: #8b8b8b; }
.container { max-width: 1200px; margin: 0 auto; padding: 24px; }
.cards { display: grid; grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); gap: 16px; margin-bottom: 24px; }
.card { background: #16213e; border-radius: 8px; padding: 20px; }
.card .label { font-size: 13px; color: #8b8b8b; margin-bottom: 8px; }
.card .value { font-size: 32px; font-weight: 600; color: #e94560; }
.card .sub { font-size: 12px; color: #8b8b8b; margin-top: 4px; }
table { width: 100%; border-collapse: collapse; background: #16213e; border-radius: 8px; overflow: hidden; }
th, td { padding: 12px 16px; text-align: left; border-bottom: 1px solid #0f3460; }
th { background: #0f3460; font-size: 13px; color: #8b8b8b; text-transform: uppercase; }
td { font-size: 14px; }
.badge { display: inline-block; padding: 2px 8px; border-radius: 4px; font-size: 12px; }
.badge-rtmp { background: #e94560; color: white; }
.badge-rtsp { background: #0f3460; color: white; }
.badge-webrtc { background: #53c0b3; color: #16213e; }
.badge-whip { background: #f5a623; color: #16213e; }
.badge-whep { background: #7b68ee; color: white; }
.badge-ws { background: #4a90d9; color: white; }
.badge-sip { background: #50c878; color: #16213e; }
.badge-srt { background: #ff6b6b; color: white; }
.badge-gb28181 { background: #9b59b6; color: white; }
.badge-mqtt { background: #3498db; color: white; }
.btn { padding: 6px 14px; border: none; border-radius: 4px; cursor: pointer; font-size: 13px; }
.btn-danger { background: #e94560; color: white; }
.btn-danger:hover { background: #c73e54; }
.btn-warn { background: #f5a623; color: #16213e; }
.btn-warn:hover { background: #d89018; }
.section-title { font-size: 18px; margin-bottom: 16px; color: #e94560; }
.empty { text-align: center; padding: 40px; color: #8b8b8b; }
#loading { text-align: center; padding: 40px; }
</style>
</head>
<body>
<div class="navbar">
<span class="logo">LingVoice</span>
<h1>Admin Panel</h1>
<span class="status" id="status">Connecting...</span>
</div>
<div class="container">
<div class="cards" id="cards">
<div class="card"><div class="label">Total Sessions</div><div class="value" id="totalSessions">-</div></div>
<div class="card"><div class="label">Protocols Active</div><div class="value" id="protoCount">-</div></div>
<div class="card"><div class="label">Uptime</div><div class="value" id="uptime">-</div></div>
</div>
<div class="section-title">Active Sessions</div>
<table>
<thead><tr><th>Session ID</th><th>Protocol</th><th>Actions</th></tr></thead>
<tbody id="sessionTable"><tr><td colspan="3" id="loading">Loading...</td></tr></tbody>
</table>
</div>
<script>
const API = window.location.pathname.replace(/\/$/, '') + '/api';
async function fetchDashboard() {
try {
const res = await fetch(API + '/dashboard');
const data = await res.json();
document.getElementById('totalSessions').textContent = data.totalSessions || 0;
const protos = Object.keys(data.byProtocol || {});
document.getElementById('protoCount').textContent = protos.length;
document.getElementById('status').textContent = 'Connected ' + new Date().toLocaleTimeString();
const tbody = document.getElementById('sessionTable');
if (!data.sessions || data.sessions.length === 0) {
tbody.innerHTML = '<tr><td colspan="3" class="empty">No active sessions</td></tr>';
return;
}
tbody.innerHTML = data.sessions.map(s => {
const badge = 'badge-' + s.protocol;
return '<tr><td>' + s.id.substring(0,8) + '...</td><td><span class="badge ' + badge + '">' + s.protocol + '</span></td>' +
'<td><button class="btn btn-warn" onclick="rejectSession(\'' + s.id + '\')">Reject</button> <button class="btn btn-danger" onclick="hangupSession(\'' + s.id + '\')">Hangup</button></td></tr>';
}).join('');
} catch(e) {
document.getElementById('status').textContent = 'Error: ' + e.message;
}
}
async function hangupSession(id) {
await fetch(API + '/sessions/' + id + '/hangup', {method:'POST'});
fetchDashboard();
}
async function rejectSession(id) {
await fetch(API + '/sessions/' + id + '/reject', {method:'POST'});
fetchDashboard();
}
fetchDashboard();
setInterval(fetchDashboard, 3000);
</script>
</body>
</html>`
