import { useState, useEffect, useRef } from "react";

// ── Fake data generators ──────────────────────────────────────────────────────
const ALGO_LABELS = { mlfq: "MLFQ", rr: "Round Robin", sjf: "SJF", fifo: "FIFO" };
const PRIORITY_COLOR = { critical: "#ff5f57", high: "#ffa657", medium: "#388bfd", low: "#7ee787" };
const STATUS_COLOR   = { running: "#00e5ff", queued: "#ffa657", completed: "#7ee787", failed: "#ff5f57", pending: "#888" };

const rand = (a, b) => Math.floor(Math.random() * (b - a + 1)) + a;
const pick = arr => arr[rand(0, arr.length - 1)];

const TASK_NAMES = [
  "image-resize-batch","ml-training-v2","log-aggregator","db-backup-pg","video-transcode",
  "report-generator","cache-warmer","data-migrator","etl-pipeline","index-builder",
  "metric-rollup","email-dispatcher","audit-scanner","snapshot-creator","cleanup-stale"
];

function makeTask(id) {
  const status = pick(["running","queued","completed","failed","pending"]);
  const priority = pick(["critical","high","medium","low"]);
  const dur = rand(5, 120);
  const progress = status === "completed" ? 100 : status === "running" ? rand(10, 90) : 0;
  return {
    id: `T-${String(id).padStart(4,"0")}`,
    name: pick(TASK_NAMES),
    status, priority,
    algo: pick(["mlfq","rr","sjf","fifo"]),
    cpu: rand(100, 4000),
    mem: rand(64, 4096),
    duration: dur,
    progress,
    worker: `worker-pod-${pick(["a","b","c","d","e"])}`,
    submitted: `${rand(0,23)}:${String(rand(0,59)).padStart(2,"0")}:${String(rand(0,59)).padStart(2,"0")}`,
    logs: [
      `[INFO]  Task ${id} initializing container...`,
      `[INFO]  Pulling image cloudos/worker:v2.1`,
      `[INFO]  Container started, PID 1`,
      `[DEBUG] Allocating ${rand(64,512)}Mi memory`,
      status === "failed" ? `[ERROR] OOMKilled: memory limit exceeded` : `[INFO]  Executing task payload...`,
    ]
  };
}

const INITIAL_TASKS = Array.from({ length: 24 }, (_, i) => makeTask(i + 100));

// ── Sparkline component ───────────────────────────────────────────────────────
function Sparkline({ data, color, height = 36 }) {
  const w = 120, h = height;
  const max = Math.max(...data), min = Math.min(...data);
  const pts = data.map((v, i) => {
    const x = (i / (data.length - 1)) * w;
    const y = h - ((v - min) / (max - min || 1)) * h;
    return `${x},${y}`;
  }).join(" ");
  const area = `0,${h} ${pts} ${w},${h}`;
  return (
    <svg width={w} height={h} style={{ overflow: "visible" }}>
      <defs>
        <linearGradient id={`sg-${color.replace("#","")}`} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor={color} stopOpacity="0.35" />
          <stop offset="100%" stopColor={color} stopOpacity="0" />
        </linearGradient>
      </defs>
      <polygon points={area} fill={`url(#sg-${color.replace("#","")})`} />
      <polyline points={pts} fill="none" stroke={color} strokeWidth="1.5" strokeLinejoin="round" />
    </svg>
  );
}

// ── Gantt Chart ───────────────────────────────────────────────────────────────
function GanttChart({ tasks }) {
  const workers = [...new Set(tasks.map(t => t.worker))].slice(0, 6);
  const totalW = 520;
  return (
    <div style={{ overflowX: "auto" }}>
      <svg width={totalW + 120} height={workers.length * 40 + 30} style={{ fontFamily: "inherit", display: "block" }}>
        {/* Worker labels */}
        {workers.map((w, i) => (
          <text key={w} x={115} y={i * 40 + 35} textAnchor="end" fill="#546e7a"
            fontSize="10" fontFamily="'JetBrains Mono', monospace">{w.replace("worker-pod-","pod-")}</text>
        ))}
        {/* Time axis */}
        {[0,25,50,75,100].map(p => (
          <g key={p}>
            <line x1={120 + p/100*totalW} y1={0} x2={120 + p/100*totalW} y2={workers.length*40+8}
              stroke="#1a2740" strokeWidth="1" />
            <text x={120 + p/100*totalW} y={workers.length*40+22} textAnchor="middle"
              fill="#546e7a" fontSize="9" fontFamily="'JetBrains Mono', monospace">{p}%</text>
          </g>
        ))}
        {/* Bars */}
        {workers.map((w, wi) => {
          const wTasks = tasks.filter(t => t.worker === w).slice(0, 4);
          let offset = 0;
          return wTasks.map((t, ti) => {
            const barW = (t.duration / 120) * totalW * 0.7;
            const x = 120 + offset;
            offset += barW + 4;
            const color = STATUS_COLOR[t.status];
            return (
              <g key={t.id}>
                <rect x={x} y={wi*40+18} width={barW} height={18} rx={2}
                  fill={color} opacity={0.18} />
                <rect x={x} y={wi*40+18} width={barW * (t.progress/100)} height={18} rx={2}
                  fill={color} opacity={0.8} />
                <text x={x+4} y={wi*40+31} fill="#fff" fontSize="9"
                  fontFamily="'JetBrains Mono', monospace" opacity={0.9}>{t.id}</text>
              </g>
            );
          });
        })}
      </svg>
    </div>
  );
}

// ── Main App ──────────────────────────────────────────────────────────────────
export default function CloudOSUI() {
  const [tasks, setTasks]           = useState(INITIAL_TASKS);
  const [activeTab, setActiveTab]   = useState("dashboard");
  const [selectedTask, setSelected] = useState(null);
  const [algo, setAlgo]             = useState("mlfq");
  const [filter, setFilter]         = useState("all");
  const [search, setSearch]         = useState("");
  const [queueDepth, setQueueDepth] = useState(Array.from({length:20}, () => rand(20,80)));
  const [cpuHistory]                = useState(Array.from({length:20}, () => rand(30,90)));
  const [submitOpen, setSubmitOpen] = useState(false);
  const [newTask, setNewTask]       = useState({ name:"", priority:"medium", cpu:500, mem:256, algo:"mlfq" });
  const [notifs, setNotifs]         = useState([]);
  const tickRef = useRef(null);

  // Simulate live updates
  useEffect(() => {
    tickRef.current = setInterval(() => {
      setTasks(prev => prev.map(t => {
        if (t.status === "running") {
          const np = Math.min(100, t.progress + rand(1, 4));
          return { ...t, progress: np, status: np >= 100 ? "completed" : "running" };
        }
        if (t.status === "queued" && Math.random() > 0.85) return { ...t, status: "running" };
        return t;
      }));
      setQueueDepth(prev => [...prev.slice(1), rand(20, 90)]);
    }, 1200);
    return () => clearInterval(tickRef.current);
  }, []);

  const addNotif = (msg, type = "info") => {
    const id = Date.now();
    setNotifs(p => [...p, { id, msg, type }]);
    setTimeout(() => setNotifs(p => p.filter(n => n.id !== id)), 3500);
  };

  const handleSubmit = () => {
    if (!newTask.name) return;
    const t = { ...makeTask(rand(200, 999)), ...newTask, id: `T-${rand(200,999)}`, status: "queued", progress: 0 };
    setTasks(p => [t, ...p]);
    setSubmitOpen(false);
    setNewTask({ name:"", priority:"medium", cpu:500, mem:256, algo:"mlfq" });
    addNotif(`Task "${t.name}" submitted to queue`, "success");
  };

  const cancelTask = id => {
    setTasks(p => p.map(t => t.id === id ? { ...t, status: "failed" } : t));
    addNotif(`Task ${id} cancelled`, "warn");
    if (selectedTask?.id === id) setSelected(null);
  };

  const filtered = tasks.filter(t => {
    if (filter !== "all" && t.status !== filter) return false;
    if (search && !t.name.includes(search) && !t.id.includes(search)) return false;
    return true;
  });

  const stats = {
    total: tasks.length,
    running: tasks.filter(t => t.status === "running").length,
    queued: tasks.filter(t => t.status === "queued").length,
    completed: tasks.filter(t => t.status === "completed").length,
    failed: tasks.filter(t => t.status === "failed").length,
  };

  // ── Styles ──────────────────────────────────────────────────────────────────
  const S = {
    root: {
      background: "#060a10",
      minHeight: "100vh",
      fontFamily: "'DM Sans', sans-serif",
      color: "#cdd9e5",
      display: "flex",
      flexDirection: "column",
    },
    // Topbar
    topbar: {
      display: "flex", alignItems: "center", justifyContent: "space-between",
      padding: "0 1.5rem", height: "52px",
      borderBottom: "1px solid #1a2740",
      background: "rgba(6,10,16,0.95)",
      backdropFilter: "blur(12px)",
      position: "sticky", top: 0, zIndex: 100,
    },
    logo: {
      fontFamily: "'Bebas Neue', sans-serif",
      fontSize: "1.5rem", letterSpacing: "0.1em",
      background: "linear-gradient(90deg, #00e5ff, #7ee787)",
      WebkitBackgroundClip: "text", WebkitTextFillColor: "transparent",
    },
    navBtn: (active) => ({
      fontFamily: "'JetBrains Mono', monospace",
      fontSize: "0.68rem", letterSpacing: "0.08em",
      padding: "0.4rem 1rem",
      background: active ? "rgba(0,229,255,0.1)" : "transparent",
      border: `1px solid ${active ? "rgba(0,229,255,0.4)" : "transparent"}`,
      borderRadius: "3px", color: active ? "#00e5ff" : "#546e7a",
      cursor: "pointer", transition: "all 0.15s",
    }),
    // Main layout
    main: { display: "flex", flex: 1, overflow: "hidden" },
    sidebar: {
      width: "52px", borderRight: "1px solid #1a2740",
      display: "flex", flexDirection: "column", alignItems: "center",
      padding: "1rem 0", gap: "0.5rem", background: "#0b1119",
    },
    sideIcon: (active) => ({
      width: "36px", height: "36px", borderRadius: "8px",
      display: "flex", alignItems: "center", justifyContent: "center",
      background: active ? "rgba(0,229,255,0.12)" : "transparent",
      border: `1px solid ${active ? "rgba(0,229,255,0.3)" : "transparent"}`,
      cursor: "pointer", fontSize: "1rem", transition: "all 0.15s",
    }),
    content: { flex: 1, overflow: "auto", padding: "1.5rem" },
    // Cards
    card: {
      background: "#0b1119", border: "1px solid #1a2740",
      borderRadius: "6px", padding: "1.2rem",
    },
    statCard: (color) => ({
      background: "#0b1119",
      border: `1px solid #1a2740`,
      borderRadius: "6px", padding: "1.2rem 1.4rem",
      position: "relative", overflow: "hidden",
      borderTop: `2px solid ${color}`,
    }),
    // Table
    th: {
      fontFamily: "'JetBrains Mono', monospace",
      fontSize: "0.6rem", letterSpacing: "0.12em",
      color: "#546e7a", textTransform: "uppercase",
      padding: "0.6rem 0.8rem", textAlign: "left",
      borderBottom: "1px solid #1a2740", fontWeight: 400,
    },
    td: {
      padding: "0.7rem 0.8rem", fontSize: "0.82rem",
      borderBottom: "1px solid #0d1628", verticalAlign: "middle",
    },
    // Badges
    badge: (color) => ({
      display: "inline-block",
      fontFamily: "'JetBrains Mono', monospace",
      fontSize: "0.58rem", letterSpacing: "0.06em",
      padding: "0.2rem 0.55rem", borderRadius: "3px",
      background: `${color}18`, border: `1px solid ${color}55`,
      color: color,
    }),
    // Input
    input: {
      background: "#0d1628", border: "1px solid #1a2740",
      borderRadius: "4px", color: "#cdd9e5",
      fontFamily: "'JetBrains Mono', monospace",
      fontSize: "0.78rem", padding: "0.5rem 0.8rem", outline: "none",
    },
    select: {
      background: "#0d1628", border: "1px solid #1a2740",
      borderRadius: "4px", color: "#cdd9e5",
      fontFamily: "'JetBrains Mono', monospace",
      fontSize: "0.78rem", padding: "0.5rem 0.8rem", outline: "none",
    },
    btn: (color = "#00e5ff") => ({
      fontFamily: "'JetBrains Mono', monospace",
      fontSize: "0.72rem", letterSpacing: "0.06em",
      padding: "0.5rem 1.2rem", borderRadius: "3px",
      background: `${color}18`, border: `1px solid ${color}55`,
      color, cursor: "pointer", transition: "all 0.15s",
    }),
    // Progress bar
    progress: (pct, color) => ({
      height: "4px", background: "#1a2740", borderRadius: "2px",
      position: "relative", overflow: "hidden", width: "80px",
    }),
    // Modal overlay
    overlay: {
      position: "fixed", inset: 0,
      background: "rgba(6,10,16,0.85)",
      backdropFilter: "blur(4px)",
      display: "flex", alignItems: "center", justifyContent: "center",
      zIndex: 200,
    },
    modal: {
      background: "#0b1119", border: "1px solid #243352",
      borderRadius: "8px", padding: "2rem", width: "440px",
      boxShadow: "0 0 60px rgba(0,229,255,0.08)",
    },
  };

  const label = (txt) => (
    <div style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: "0.6rem",
      color: "#546e7a", letterSpacing: "0.12em", textTransform: "uppercase", marginBottom: "0.4rem" }}>
      {txt}
    </div>
  );

  // ── DASHBOARD TAB ────────────────────────────────────────────────────────────
  const DashboardTab = () => (
    <div>
      {/* Stat cards */}
      <div style={{ display: "grid", gridTemplateColumns: "repeat(5, 1fr)", gap: "0.8rem", marginBottom: "1.2rem" }}>
        {[
          { label: "Total Tasks", val: stats.total, color: "#cdd9e5", data: cpuHistory },
          { label: "Running",     val: stats.running, color: "#00e5ff", data: queueDepth },
          { label: "Queued",      val: stats.queued,  color: "#ffa657", data: cpuHistory.map(v=>v*0.6) },
          { label: "Completed",   val: stats.completed, color: "#7ee787", data: cpuHistory.map(v=>v*0.8) },
          { label: "Failed",      val: stats.failed,  color: "#ff5f57", data: cpuHistory.map(v=>v*0.2) },
        ].map(s => (
          <div key={s.label} style={S.statCard(s.color)}>
            <div style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: "0.58rem",
              color: "#546e7a", letterSpacing: "0.12em", textTransform: "uppercase", marginBottom: "0.4rem" }}>
              {s.label}
            </div>
            <div style={{ display: "flex", justifyContent: "space-between", alignItems: "flex-end" }}>
              <span style={{ fontSize: "2rem", fontWeight: 700, color: s.color, lineHeight: 1 }}>{s.val}</span>
              <Sparkline data={s.data} color={s.color} height={32} />
            </div>
          </div>
        ))}
      </div>

      {/* Two columns: Gantt + Algo selector */}
      <div style={{ display: "grid", gridTemplateColumns: "1fr 280px", gap: "0.8rem", marginBottom: "1.2rem" }}>
        <div style={S.card}>
          <div style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: "0.62rem",
            color: "#546e7a", letterSpacing: "0.14em", textTransform: "uppercase", marginBottom: "1rem" }}>
            ◈ Task Gantt Timeline
          </div>
          <GanttChart tasks={tasks} />
        </div>

        <div style={S.card}>
          <div style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: "0.62rem",
            color: "#546e7a", letterSpacing: "0.14em", textTransform: "uppercase", marginBottom: "1rem" }}>
            ◈ Scheduling Algorithm
          </div>
          {["mlfq","rr","sjf","fifo"].map(a => (
            <div key={a} onClick={() => { setAlgo(a); addNotif(`Switched to ${ALGO_LABELS[a]}`, "info"); }}
              style={{
                padding: "0.8rem 1rem", borderRadius: "4px", marginBottom: "0.5rem", cursor: "pointer",
                background: algo === a ? "rgba(0,229,255,0.07)" : "rgba(255,255,255,0.02)",
                border: `1px solid ${algo === a ? "rgba(0,229,255,0.4)" : "#1a2740"}`,
                transition: "all 0.15s",
              }}>
              <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
                <span style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: "0.78rem",
                  color: algo === a ? "#00e5ff" : "#cdd9e5", fontWeight: algo === a ? 700 : 400 }}>
                  {ALGO_LABELS[a]}
                </span>
                {algo === a && <span style={{ fontSize: "0.6rem", color: "#00e5ff" }}>● ACTIVE</span>}
              </div>
              <div style={{ fontSize: "0.7rem", color: "#546e7a", marginTop: "0.2rem", lineHeight: 1.4 }}>
                {{ mlfq: "Multi-level feedback queue — adapts priority dynamically",
                   rr:   "Round Robin — equal time slices per task",
                   sjf:  "Shortest Job First — minimizes avg wait time",
                   fifo: "First In First Out — simple queue order" }[a]}
              </div>
            </div>
          ))}
        </div>
      </div>

      {/* Queue depth chart */}
      <div style={S.card}>
        <div style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: "0.62rem",
          color: "#546e7a", letterSpacing: "0.14em", textTransform: "uppercase", marginBottom: "0.8rem" }}>
          ◈ Live Queue Depth
        </div>
        <svg width="100%" height="80" viewBox="0 0 600 80" preserveAspectRatio="none">
          <defs>
            <linearGradient id="qg" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="#00e5ff" stopOpacity="0.3" />
              <stop offset="100%" stopColor="#00e5ff" stopOpacity="0" />
            </linearGradient>
          </defs>
          {(() => {
            const pts = queueDepth.map((v, i) => {
              const x = (i / (queueDepth.length - 1)) * 600;
              const y = 80 - (v / 100) * 72;
              return `${x},${y}`;
            }).join(" ");
            const area = `0,80 ${pts} 600,80`;
            return (
              <>
                <polygon points={area} fill="url(#qg)" />
                <polyline points={pts} fill="none" stroke="#00e5ff" strokeWidth="2" strokeLinejoin="round" />
              </>
            );
          })()}
        </svg>
      </div>
    </div>
  );

  // ── TASKS TAB ────────────────────────────────────────────────────────────────
  const TasksTab = () => (
    <div>
      {/* Toolbar */}
      <div style={{ display: "flex", gap: "0.6rem", marginBottom: "1rem", alignItems: "center", flexWrap: "wrap" }}>
        <input value={search} onChange={e => setSearch(e.target.value)}
          placeholder="Search tasks..." style={{ ...S.input, width: "200px" }} />
        {["all","running","queued","completed","failed"].map(f => (
          <button key={f} onClick={() => setFilter(f)} style={S.navBtn(filter === f)}>
            {f.toUpperCase()}
          </button>
        ))}
        <div style={{ flex: 1 }} />
        <button onClick={() => setSubmitOpen(true)} style={{
          ...S.btn("#7ee787"),
          fontWeight: 700, display: "flex", alignItems: "center", gap: "0.4rem"
        }}>
          + SUBMIT TASK
        </button>
      </div>

      {/* Table + detail pane */}
      <div style={{ display: "grid", gridTemplateColumns: selectedTask ? "1fr 340px" : "1fr", gap: "0.8rem" }}>
        <div style={{ ...S.card, padding: 0, overflow: "hidden" }}>
          <table style={{ width: "100%", borderCollapse: "collapse" }}>
            <thead>
              <tr style={{ background: "#0d1628" }}>
                {["ID","Name","Status","Priority","Algorithm","Progress","Worker","Action"].map(h => (
                  <th key={h} style={S.th}>{h}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {filtered.map(t => (
                <tr key={t.id}
                  onClick={() => setSelected(selectedTask?.id === t.id ? null : t)}
                  style={{
                    cursor: "pointer",
                    background: selectedTask?.id === t.id ? "rgba(0,229,255,0.04)" : "transparent",
                    transition: "background 0.15s",
                  }}
                  onMouseEnter={e => e.currentTarget.style.background = "rgba(255,255,255,0.02)"}
                  onMouseLeave={e => e.currentTarget.style.background = selectedTask?.id === t.id ? "rgba(0,229,255,0.04)" : "transparent"}
                >
                  <td style={S.td}>
                    <span style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: "0.72rem", color: "#546e7a" }}>{t.id}</span>
                  </td>
                  <td style={S.td}>
                    <span style={{ fontSize: "0.82rem", fontWeight: 500 }}>{t.name}</span>
                  </td>
                  <td style={S.td}>
                    <span style={S.badge(STATUS_COLOR[t.status])}>{t.status}</span>
                  </td>
                  <td style={S.td}>
                    <span style={S.badge(PRIORITY_COLOR[t.priority])}>{t.priority}</span>
                  </td>
                  <td style={S.td}>
                    <span style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: "0.68rem", color: "#546e7a" }}>
                      {ALGO_LABELS[t.algo]}
                    </span>
                  </td>
                  <td style={S.td}>
                    <div style={{ display: "flex", alignItems: "center", gap: "0.5rem" }}>
                      <div style={S.progress(t.progress)}>
                        <div style={{ position: "absolute", inset: 0,
                          width: `${t.progress}%`, borderRadius: "2px",
                          background: STATUS_COLOR[t.status], transition: "width 0.5s" }} />
                      </div>
                      <span style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: "0.65rem", color: "#546e7a" }}>
                        {t.progress}%
                      </span>
                    </div>
                  </td>
                  <td style={S.td}>
                    <span style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: "0.68rem", color: "#546e7a" }}>
                      {t.worker.replace("worker-pod-","pod-")}
                    </span>
                  </td>
                  <td style={S.td} onClick={e => e.stopPropagation()}>
                    {t.status !== "completed" && t.status !== "failed" && (
                      <button onClick={() => cancelTask(t.id)} style={{
                        ...S.btn("#ff5f57"), padding: "0.25rem 0.7rem", fontSize: "0.62rem"
                      }}>CANCEL</button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        {/* Detail pane */}
        {selectedTask && (
          <div style={{ ...S.card, fontSize: "0.82rem" }}>
            <div style={{ display: "flex", justifyContent: "space-between", alignItems: "flex-start", marginBottom: "1.2rem" }}>
              <div>
                <div style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: "0.62rem", color: "#546e7a", marginBottom: "0.3rem" }}>
                  TASK DETAILS
                </div>
                <div style={{ fontFamily: "'Bebas Neue', sans-serif", fontSize: "1.5rem", letterSpacing: "0.05em" }}>
                  {selectedTask.id}
                </div>
              </div>
              <button onClick={() => setSelected(null)} style={{ ...S.btn("#546e7a"), padding: "0.3rem 0.6rem" }}>✕</button>
            </div>

            {[
              ["Name", selectedTask.name],
              ["Status", selectedTask.status],
              ["Priority", selectedTask.priority],
              ["Algorithm", ALGO_LABELS[selectedTask.algo]],
              ["Worker", selectedTask.worker],
              ["CPU Request", `${selectedTask.cpu}m`],
              ["Memory", `${selectedTask.mem}Mi`],
              ["Duration", `${selectedTask.duration}s`],
              ["Submitted", selectedTask.submitted],
            ].map(([k, v]) => (
              <div key={k} style={{ display: "flex", justifyContent: "space-between",
                padding: "0.5rem 0", borderBottom: "1px solid #0d1628" }}>
                <span style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: "0.65rem", color: "#546e7a" }}>{k}</span>
                <span style={{ fontSize: "0.78rem", fontWeight: 500 }}>{v}</span>
              </div>
            ))}

            {/* Progress */}
            <div style={{ margin: "1rem 0 0.5rem" }}>
              <div style={{ display: "flex", justifyContent: "space-between", marginBottom: "0.4rem" }}>
                <span style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: "0.62rem", color: "#546e7a" }}>PROGRESS</span>
                <span style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: "0.62rem",
                  color: STATUS_COLOR[selectedTask.status] }}>{selectedTask.progress}%</span>
              </div>
              <div style={{ height: "6px", background: "#1a2740", borderRadius: "3px", overflow: "hidden" }}>
                <div style={{ height: "100%", width: `${selectedTask.progress}%`,
                  background: STATUS_COLOR[selectedTask.status],
                  borderRadius: "3px", transition: "width 0.5s",
                  boxShadow: `0 0 8px ${STATUS_COLOR[selectedTask.status]}` }} />
              </div>
            </div>

            {/* Logs */}
            <div style={{ marginTop: "1rem" }}>
              <div style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: "0.62rem",
                color: "#546e7a", letterSpacing: "0.1em", textTransform: "uppercase", marginBottom: "0.5rem" }}>
                LOGS
              </div>
              <div style={{ background: "#060a10", border: "1px solid #1a2740", borderRadius: "4px",
                padding: "0.8rem", maxHeight: "150px", overflowY: "auto" }}>
                {selectedTask.logs.map((l, i) => (
                  <div key={i} style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: "0.65rem",
                    color: l.includes("ERROR") ? "#ff5f57" : l.includes("DEBUG") ? "#546e7a" : "#7ee787",
                    lineHeight: 1.7 }}>{l}</div>
                ))}
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  );

  // ── WORKERS TAB ──────────────────────────────────────────────────────────────
  const WorkersTab = () => {
    const workers = ["a","b","c","d","e"].map(id => ({
      id: `worker-pod-${id}`,
      cpu: rand(20, 95),
      mem: rand(30, 90),
      tasks: tasks.filter(t => t.worker === `worker-pod-${id}`).length,
      status: pick(["healthy","healthy","healthy","degraded"]),
    }));
    return (
      <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(280px, 1fr))", gap: "0.8rem" }}>
        {workers.map(w => (
          <div key={w.id} style={{ ...S.card, borderTop: `2px solid ${w.status === "healthy" ? "#7ee787" : "#ffa657"}` }}>
            <div style={{ display: "flex", justifyContent: "space-between", alignItems: "flex-start", marginBottom: "1rem" }}>
              <div>
                <div style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: "0.65rem", color: "#546e7a" }}>WORKER NODE</div>
                <div style={{ fontFamily: "'Bebas Neue', sans-serif", fontSize: "1.3rem", letterSpacing: "0.05em" }}>{w.id}</div>
              </div>
              <span style={S.badge(w.status === "healthy" ? "#7ee787" : "#ffa657")}>{w.status}</span>
            </div>
            {[["CPU Usage", w.cpu, "#00e5ff"], ["Memory Usage", w.mem, "#7b5cf0"]].map(([label, val, color]) => (
              <div key={label} style={{ marginBottom: "0.8rem" }}>
                <div style={{ display: "flex", justifyContent: "space-between", marginBottom: "0.3rem" }}>
                  <span style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: "0.62rem", color: "#546e7a" }}>{label}</span>
                  <span style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: "0.62rem", color }}>{val}%</span>
                </div>
                <div style={{ height: "5px", background: "#1a2740", borderRadius: "3px", overflow: "hidden" }}>
                  <div style={{ height: "100%", width: `${val}%`, background: color, borderRadius: "3px",
                    boxShadow: val > 80 ? `0 0 6px ${color}` : "none" }} />
                </div>
              </div>
            ))}
            <div style={{ display: "flex", gap: "1rem", marginTop: "0.5rem" }}>
              <div style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: "0.65rem", color: "#546e7a" }}>
                TASKS: <span style={{ color: "#cdd9e5" }}>{w.tasks}</span>
              </div>
            </div>
          </div>
        ))}
      </div>
    );
  };

  // ── RENDER ────────────────────────────────────────────────────────────────────
  return (
    <>
      <style>{`
        @import url('https://fonts.googleapis.com/css2?family=JetBrains+Mono:wght@300;400;700&family=Bebas+Neue&family=DM+Sans:wght@300;400;500;700&display=swap');
        * { box-sizing: border-box; margin: 0; padding: 0; }
        ::-webkit-scrollbar { width: 4px; height: 4px; }
        ::-webkit-scrollbar-track { background: #060a10; }
        ::-webkit-scrollbar-thumb { background: #1a2740; border-radius: 2px; }
        body { background: #060a10; }
        @keyframes fadein { from { opacity:0; transform:translateY(6px); } to { opacity:1; transform:none; } }
        @keyframes slideIn { from { opacity:0; transform:translateX(20px); } to { opacity:1; transform:none; } }
        .notif { animation: slideIn 0.3s ease; }
      `}</style>

      <div style={S.root}>
        {/* Topbar */}
        <div style={S.topbar}>
          <div style={{ display: "flex", alignItems: "center", gap: "1.5rem" }}>
            <div style={S.logo}>CLOUDOS</div>
            <div style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: "0.6rem",
              color: "#243352", letterSpacing: "0.1em" }}>SCHEDULER v2.1</div>
          </div>
          <div style={{ display: "flex", gap: "0.4rem" }}>
            {[["dashboard","◈ Dashboard"],["tasks","◈ Tasks"],["workers","◈ Workers"]].map(([id, label]) => (
              <button key={id} onClick={() => setActiveTab(id)} style={S.navBtn(activeTab === id)}>{label}</button>
            ))}
          </div>
          <div style={{ display: "flex", alignItems: "center", gap: "1rem" }}>
            <div style={{ display: "flex", alignItems: "center", gap: "0.4rem" }}>
              <div style={{ width: 7, height: 7, borderRadius: "50%", background: "#7ee787",
                boxShadow: "0 0 6px #7ee787", animation: "pulse 2s ease infinite" }} />
              <span style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: "0.62rem", color: "#546e7a" }}>
                CLUSTER HEALTHY
              </span>
            </div>
            <div style={S.badge("#388bfd")}>{ALGO_LABELS[algo]}</div>
          </div>
        </div>

        <div style={S.main}>
          {/* Sidebar icons */}
          <div style={S.sidebar}>
            {[
              ["dashboard","📊"],["tasks","📋"],["workers","⚙️"],
            ].map(([id, icon]) => (
              <div key={id} onClick={() => setActiveTab(id)} style={S.sideIcon(activeTab === id)}
                title={id}>{icon}</div>
            ))}
            <div style={{ flex: 1 }} />
            <div style={S.sideIcon(false)} title="Settings">⚙</div>
          </div>

          {/* Content */}
          <div style={S.content}>
            {activeTab === "dashboard" && <DashboardTab />}
            {activeTab === "tasks"     && <TasksTab />}
            {activeTab === "workers"   && <WorkersTab />}
          </div>
        </div>

        {/* Submit Task Modal */}
        {submitOpen && (
          <div style={S.overlay} onClick={() => setSubmitOpen(false)}>
            <div style={S.modal} onClick={e => e.stopPropagation()}>
              <div style={{ fontFamily: "'Bebas Neue', sans-serif", fontSize: "1.8rem",
                letterSpacing: "0.05em", marginBottom: "1.5rem",
                background: "linear-gradient(90deg, #00e5ff, #7ee787)",
                WebkitBackgroundClip: "text", WebkitTextFillColor: "transparent" }}>
                SUBMIT NEW TASK
              </div>

              {[
                { l: "Task Name", field: "name", type: "input", placeholder: "e.g. ml-training-v3" },
              ].map(({ l, field, placeholder }) => (
                <div key={field} style={{ marginBottom: "1rem" }}>
                  {label(l)}
                  <input value={newTask[field]} onChange={e => setNewTask(p => ({ ...p, [field]: e.target.value }))}
                    placeholder={placeholder} style={{ ...S.input, width: "100%" }} />
                </div>
              ))}

              <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "0.8rem", marginBottom: "1rem" }}>
                <div>
                  {label("Priority")}
                  <select value={newTask.priority} onChange={e => setNewTask(p => ({ ...p, priority: e.target.value }))}
                    style={{ ...S.select, width: "100%" }}>
                    {["critical","high","medium","low"].map(p => <option key={p} value={p}>{p}</option>)}
                  </select>
                </div>
                <div>
                  {label("Algorithm")}
                  <select value={newTask.algo} onChange={e => setNewTask(p => ({ ...p, algo: e.target.value }))}
                    style={{ ...S.select, width: "100%" }}>
                    {Object.entries(ALGO_LABELS).map(([k,v]) => <option key={k} value={k}>{v}</option>)}
                  </select>
                </div>
                <div>
                  {label(`CPU (millicores) — ${newTask.cpu}m`)}
                  <input type="range" min={100} max={4000} step={100}
                    value={newTask.cpu} onChange={e => setNewTask(p => ({ ...p, cpu: +e.target.value }))}
                    style={{ width: "100%", accentColor: "#00e5ff" }} />
                </div>
                <div>
                  {label(`Memory — ${newTask.mem}Mi`)}
                  <input type="range" min={64} max={4096} step={64}
                    value={newTask.mem} onChange={e => setNewTask(p => ({ ...p, mem: +e.target.value }))}
                    style={{ width: "100%", accentColor: "#7ee787" }} />
                </div>
              </div>

              <div style={{ display: "flex", gap: "0.8rem", justifyContent: "flex-end" }}>
                <button onClick={() => setSubmitOpen(false)} style={S.btn("#546e7a")}>CANCEL</button>
                <button onClick={handleSubmit} style={{ ...S.btn("#7ee787"), fontWeight: 700 }}>SUBMIT →</button>
              </div>
            </div>
          </div>
        )}

        {/* Notifications */}
        <div style={{ position: "fixed", bottom: "1.5rem", right: "1.5rem", display: "flex",
          flexDirection: "column", gap: "0.5rem", zIndex: 300 }}>
          {notifs.map(n => (
            <div key={n.id} className="notif" style={{
              fontFamily: "'JetBrains Mono', monospace", fontSize: "0.72rem",
              padding: "0.7rem 1.2rem", borderRadius: "4px",
              background: "#0b1119",
              border: `1px solid ${{ success:"#7ee787", warn:"#ffa657", info:"#388bfd" }[n.type]}`,
              color: { success:"#7ee787", warn:"#ffa657", info:"#388bfd" }[n.type],
              boxShadow: "0 4px 20px rgba(0,0,0,0.4)",
            }}>{n.msg}</div>
          ))}
        </div>
      </div>
    </>
  );
}
