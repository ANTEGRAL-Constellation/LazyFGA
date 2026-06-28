/** lazyfga-0: 부팅 확인용 빈 화면. 후속 명세(model-canvas 등)가 라우팅/화면을 채운다. */
export function App() {
  return (
    <main
      style={{
        fontFamily: "system-ui, sans-serif",
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        justifyContent: "center",
        minHeight: "100vh",
        gap: "0.5rem",
        color: "#1f2937",
      }}
    >
      <h1 style={{ margin: 0, fontSize: "1.5rem" }}>lazyFGA</h1>
      <p style={{ margin: 0, color: "#6b7280" }}>control plane — scaffold</p>
    </main>
  );
}
