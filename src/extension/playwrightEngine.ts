import { killAll, launch } from "chrome-launcher";
import { type Browser, type BrowserContext, type Page, chromium } from "playwright-core";
import * as vscode from "vscode";
import type { MessageEnvelope } from "../shared/protocol.js";
import type { EngineTransport } from "./engineTransport.js";

export interface PlaywrightEngineOptions {
  outputChannel: vscode.OutputChannel;
}

interface PageHandle {
  projectId: string;
  page: Page;
  transport: PlaywrightEngineTransport;
}

class PlaywrightEngineTransport implements EngineTransport {
  private _onDidReceiveMessage = new vscode.EventEmitter<MessageEnvelope>();
  public readonly onDidReceiveMessage = this._onDidReceiveMessage.event;

  private _onDidDispose = new vscode.EventEmitter<void>();
  public readonly onDidDispose = this._onDidDispose.event;

  private disposed = false;

  constructor(
    private page: Page,
    private projectId: string,
  ) {}

  postMessage(message: MessageEnvelope): void {
    if (this.disposed) return;
    void this.page
      .evaluate((envelope) => {
        const receive = (window as unknown as Record<string, unknown>)
          .vsdawReceiveMessage as unknown as ((msg: MessageEnvelope) => void) | undefined;
        if (typeof receive === "function") {
          receive(envelope);
        }
      }, message)
      .catch((error) => {
        console.warn(`[playwright] failed to deliver message to ${this.projectId}:`, error);
      });
  }

  receiveMessage(message: MessageEnvelope): void {
    if (this.disposed) return;
    this._onDidReceiveMessage.fire(message);
  }

  dispose(): void {
    if (this.disposed) return;
    this.disposed = true;
    this._onDidReceiveMessage.dispose();
    this._onDidDispose.fire();
    this._onDidDispose.dispose();
  }
}

export class PlaywrightEngineManager implements vscode.Disposable {
  private browser: Browser | undefined;
  private context: BrowserContext | undefined;
  private pages = new Map<string, PageHandle>();
  private _onDidChange = new vscode.EventEmitter<void>();
  public readonly onDidChange = this._onDidChange.event;
  private starting = false;

  constructor(private options: PlaywrightEngineOptions) {}

  get isRunning(): boolean {
    return this.browser !== undefined && this.context !== undefined;
  }

  get projectCount(): number {
    return this.pages.size;
  }

  async start(): Promise<void> {
    if (this.isRunning || this.starting) return;
    this.starting = true;
    try {
      const chrome = await launch({
        startingUrl: "about:blank",
        chromeFlags: [
          "--headless=new",
          "--no-sandbox",
          "--disable-setuid-sandbox",
          "--disable-gpu",
          "--disable-dev-shm-usage",
          "--disable-background-timer-throttling",
          "--disable-renderer-backgrounding",
          "--window-size=1280,720",
        ],
      });

      this.browser = await chromium.connectOverCDP(`http://127.0.0.1:${chrome.port}`);
      this.context = this.browser.contexts()[0] ?? (await this.browser.newContext());
      this.options.outputChannel.appendLine(
        `[playwright] connected to Chrome on port ${chrome.port}`,
      );
      this._onDidChange.fire();
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      this.options.outputChannel.appendLine(`[playwright] failed to start: ${message}`);
      throw error;
    } finally {
      this.starting = false;
    }
  }

  async stop(): Promise<void> {
    for (const handle of this.pages.values()) {
      handle.transport.dispose();
      await handle.page.close().catch(() => {
        // ignore
      });
    }
    this.pages.clear();

    if (this.context) {
      await this.context.close().catch(() => {
        // ignore
      });
      this.context = undefined;
    }

    if (this.browser) {
      await this.browser.close().catch(() => {
        // ignore
      });
      this.browser = undefined;
    }

    try {
      killAll();
    } catch {
      // ignore
    }

    this.options.outputChannel.appendLine("[playwright] stopped");
    this._onDidChange.fire();
  }

  async createEngine(projectId: string, origin: string): Promise<EngineTransport> {
    if (!this.context) {
      await this.start();
    }
    if (!this.context) {
      throw new Error("Playwright engine context is not available");
    }

    const existing = this.pages.get(projectId);
    if (existing) {
      return existing.transport;
    }

    const page = await this.context.newPage();
    const url = `${origin}/engine?projectId=${encodeURIComponent(projectId)}`;

    const transport = new PlaywrightEngineTransport(page, projectId);

    await page.exposeFunction("vsdawSend", (raw: unknown) => {
      if (raw && typeof raw === "object") {
        transport.receiveMessage(raw as unknown as MessageEnvelope);
      }
    });

    await page.goto(url, { waitUntil: "load" });

    const handle: PageHandle = { projectId, page, transport };
    this.pages.set(projectId, handle);

    page.on("close", () => {
      this.pages.delete(projectId);
      transport.dispose();
      this._onDidChange.fire();
    });

    this._onDidChange.fire();
    return transport;
  }

  dispose(): void {
    void this.stop();
    this._onDidChange.dispose();
  }
}
