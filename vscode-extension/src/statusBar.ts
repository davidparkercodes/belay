import * as vscode from "vscode";
import { BelayClient } from "./belayClient";

const POLL_INTERVAL_MS = 10_000;

export class BelayStatusBar {
  private item: vscode.StatusBarItem;
  private timer: ReturnType<typeof setInterval> | undefined;
  private connected = false;

  constructor(private client: BelayClient) {
    this.item = vscode.window.createStatusBarItem(
      vscode.StatusBarAlignment.Left,
      50
    );
    this.item.command = "workbench.view.extension.belay";
    this.item.name = "Belay Status";
    this.setDisconnected();
    this.item.show();
  }

  /**
   * Start polling the Belay daemon for health status.
   */
  startPolling(): void {
    // Check immediately on startup
    this.checkHealth();

    this.timer = setInterval(() => {
      this.checkHealth();
    }, POLL_INTERVAL_MS);
  }

  /**
   * Stop polling and dispose the status bar item.
   */
  dispose(): void {
    if (this.timer) {
      clearInterval(this.timer);
      this.timer = undefined;
    }
    this.item.dispose();
  }

  /**
   * Whether the daemon is currently connected.
   */
  isConnected(): boolean {
    return this.connected;
  }

  private async checkHealth(): Promise<void> {
    try {
      const health = await this.client.getHealth();
      if (health.status === "ok") {
        this.setConnected(health.version);
      } else {
        this.setDisconnected();
      }
    } catch {
      this.setDisconnected();
    }
  }

  private setConnected(version: string): void {
    this.connected = true;
    this.item.text = "$(shield) Belay: Connected";
    this.item.tooltip = `Belay daemon v${version} is running. Click to open sidebar.`;
    this.item.backgroundColor = undefined;
  }

  private setDisconnected(): void {
    this.connected = false;
    this.item.text = "$(shield) Belay: Disconnected";
    this.item.tooltip =
      "Belay daemon is not running. Start it with `belay daemon start`.";
    this.item.backgroundColor = new vscode.ThemeColor(
      "statusBarItem.warningBackground"
    );
  }
}
