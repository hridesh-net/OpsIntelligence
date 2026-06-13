import { Component, type ReactNode } from "react";

/* A crash in any screen used to blank the entire Scrun tab (white screen).
   This boundary contains the failure to the screen area and shows a recovery
   path back to the boards gallery instead. */
interface Props {
  children: ReactNode;
  onReset?: () => void;
}
interface State {
  error: Error | null;
}

export default class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error) {
    console.error("[scrun] screen crashed:", error);
  }

  render() {
    if (!this.state.error) return this.props.children;
    return (
      <div
        style={{
          minHeight: "60vh",
          display: "flex",
          flexDirection: "column",
          alignItems: "center",
          justifyContent: "center",
          gap: 14,
          textAlign: "center",
          padding: 32,
        }}
      >
        <div style={{ fontSize: 18, fontWeight: 700, color: "var(--text)" }}>
          This board hit an unexpected error.
        </div>
        <div style={{ fontSize: 13.5, color: "var(--text-dim)", maxWidth: 420, lineHeight: 1.6 }}>
          {this.state.error.message || "Something went wrong while rendering the board."}
        </div>
        <div style={{ display: "flex", gap: 10 }}>
          <button
            className="btn"
            onClick={() => {
              this.setState({ error: null });
              this.props.onReset?.();
            }}
          >
            Back to boards
          </button>
          <button className="btn" onClick={() => this.setState({ error: null })}>
            Try again
          </button>
        </div>
      </div>
    );
  }
}
