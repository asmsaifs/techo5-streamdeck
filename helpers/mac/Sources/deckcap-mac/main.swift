// deckcap-mac: the macOS capture helper. Protocol: docs/helpers.md.
//
// Lists windows, captures one with ScreenCaptureKit (the window as it is, even when covered, plus
// its app's sound) and posts mouse events into it. Screen Recording is needed to capture and list;
// Accessibility to post input. stdout carries only framed messages, so logs go to stderr.

import AppKit
import CoreMedia
import Foundation
import ScreenCaptureKit

// MARK: output

let outLock = NSLock()

func send(_ kind: UInt8, _ payload: Data) {
    var msg = Data(capacity: payload.count + 5)
    var n = UInt32(payload.count).littleEndian
    withUnsafeBytes(of: &n) { msg.append(contentsOf: $0) }
    msg.append(kind)
    msg.append(payload)
    outLock.lock(); defer { outLock.unlock() }
    FileHandle.standardOutput.write(msg)
}

func sendEvent(_ event: String, code: String = "", msg: String = "") {
    var o: [String: String] = ["event": event]
    if !code.isEmpty { o["code"] = code }
    if !msg.isEmpty { o["msg"] = msg }
    send(4, (try? JSONSerialization.data(withJSONObject: o)) ?? Data())
}

func log(_ s: String) { FileHandle.standardError.write(Data((s + "\n").utf8)) }

// MARK: capture

final class Capture: NSObject, SCStreamOutput, SCStreamDelegate {
    let window: SCWindow
    var stream: SCStream?
    // Size of the last frame sent, in pixels: input coordinates are relative to it.
    private let lock = NSLock()
    private var frameW = 0, frameH = 0

    init(window: SCWindow) { self.window = window }

    func start(fps: Int, maxW: Int, audio: Bool) async throws {
        let cfg = SCStreamConfiguration()
        let scale = min(1, Double(maxW) / window.frame.width)
        cfg.width = max(2, Int(window.frame.width * scale))
        cfg.height = max(2, Int(window.frame.height * scale))
        cfg.minimumFrameInterval = CMTime(value: 1, timescale: CMTimeScale(max(1, fps)))
        cfg.pixelFormat = kCVPixelFormatType_32BGRA
        cfg.showsCursor = false
        cfg.queueDepth = 5
        if audio {
            cfg.capturesAudio = true
            cfg.sampleRate = 48000
            cfg.channelCount = 2
            cfg.excludesCurrentProcessAudio = true
        }
        let s = SCStream(filter: SCContentFilter(desktopIndependentWindow: window), configuration: cfg, delegate: self)
        let q = DispatchQueue(label: "deckcap.capture")
        try s.addStreamOutput(self, type: .screen, sampleHandlerQueue: q)
        if audio { try s.addStreamOutput(self, type: .audio, sampleHandlerQueue: q) }
        try await s.startCapture()
        stream = s
    }

    func stop() async {
        let s = stream
        stream = nil
        try? await s?.stopCapture()
    }

    var frameSize: (Int, Int) { lock.lock(); defer { lock.unlock() }; return (frameW, frameH) }

    func stream(_ stream: SCStream, didOutputSampleBuffer sb: CMSampleBuffer, of type: SCStreamOutputType) {
        guard sb.isValid else { return }
        switch type {
        case .screen: sendFrame(sb)
        case .audio: sendAudio(sb)
        default: break
        }
    }

    func stream(_ stream: SCStream, didStopWithError error: Error) {
        // The window closing is the usual reason; the core goes back to the deck on no_window.
        sendEvent("error", code: "no_window", msg: "\(error.localizedDescription)")
        self.stream = nil
    }

    private func sendFrame(_ sb: CMSampleBuffer) {
        // Only .complete frames carry a new picture; the others say the window did not change.
        guard let att = CMSampleBufferGetSampleAttachmentsArray(sb, createIfNecessary: false) as? [[SCStreamFrameInfo: Any]],
              let raw = att.first?[.status] as? Int, SCFrameStatus(rawValue: raw) == .complete,
              let img = sb.imageBuffer else { return }
        CVPixelBufferLockBaseAddress(img, .readOnly)
        defer { CVPixelBufferUnlockBaseAddress(img, .readOnly) }
        let w = CVPixelBufferGetWidth(img), h = CVPixelBufferGetHeight(img)
        guard w > 0, h > 0, w < 65536, h < 65536, let base = CVPixelBufferGetBaseAddress(img) else { return }
        let stride = CVPixelBufferGetBytesPerRow(img)
        var d = Data(capacity: 4 + w * h * 4)
        var hdr = [UInt8(w & 255), UInt8(w >> 8), UInt8(h & 255), UInt8(h >> 8)]
        d.append(&hdr, count: 4)
        // The buffer rows are padded; the protocol has none.
        for y in 0..<h { d.append(Data(bytes: base + y * stride, count: w * 4)) }
        lock.lock(); frameW = w; frameH = h; lock.unlock()
        send(2, d)
    }

    private func sendAudio(_ sb: CMSampleBuffer) {
        guard let fd = sb.formatDescription,
              let asbd = CMAudioFormatDescriptionGetStreamBasicDescription(fd)?.pointee,
              asbd.mFormatID == kAudioFormatLinearPCM, asbd.mBitsPerChannel == 32,
              asbd.mFormatFlags & kAudioFormatFlagIsFloat != 0 else { return }
        let ch = Int(asbd.mChannelsPerFrame)
        let frames = CMSampleBufferGetNumSamples(sb)
        guard ch > 0, frames > 0 else { return }
        let nonInterleaved = asbd.mFormatFlags & kAudioFormatFlagIsNonInterleaved != 0
        var size = 0
        CMSampleBufferGetAudioBufferListWithRetainedBlockBuffer(
            sb, bufferListSizeNeededOut: &size, bufferListOut: nil, bufferListSize: 0,
            blockBufferAllocator: nil, blockBufferMemoryAllocator: nil, flags: 0, blockBufferOut: nil)
        let raw = UnsafeMutableRawPointer.allocate(byteCount: size, alignment: 16)
        defer { raw.deallocate() }
        let abl = raw.bindMemory(to: AudioBufferList.self, capacity: 1)
        var block: CMBlockBuffer?
        guard CMSampleBufferGetAudioBufferListWithRetainedBlockBuffer(
            sb, bufferListSizeNeededOut: nil, bufferListOut: abl, bufferListSize: size,
            blockBufferAllocator: nil, blockBufferMemoryAllocator: nil, flags: 0, blockBufferOut: &block) == noErr
        else { return }
        let bufs = UnsafeMutableAudioBufferListPointer(abl)
        var out = [Int16](repeating: 0, count: frames * ch)
        @inline(__always) func s16(_ f: Float) -> Int16 { Int16(max(-1, min(1, f)) * 32767) }
        for c in 0..<ch {
            if nonInterleaved {
                guard c < bufs.count, let p = bufs[c].mData?.assumingMemoryBound(to: Float.self) else { return }
                for i in 0..<min(frames, Int(bufs[c].mDataByteSize) / 4) { out[i * ch + c] = s16(p[i]) }
            } else {
                guard let p = bufs[0].mData?.assumingMemoryBound(to: Float.self) else { return }
                for i in 0..<min(frames, Int(bufs[0].mDataByteSize) / (4 * ch)) { out[i * ch + c] = s16(p[i * ch + c]) }
            }
        }
        var d = Data(capacity: 5 + out.count * 2)
        var rate = UInt32(asbd.mSampleRate).littleEndian
        withUnsafeBytes(of: &rate) { d.append(contentsOf: $0) }
        d.append(UInt8(ch))
        out.withUnsafeBytes { d.append(contentsOf: $0) } // little-endian host
        send(3, d)
    }
}

// MARK: input

/// Posts mouse events into a captured window. x,y are in the pixels of the last frame.
struct Input {
    var dragging = false

    func point(_ cap: Capture, _ x: Int, _ y: Int) -> CGPoint? {
        let (fw, fh) = cap.frameSize
        guard fw > 0, fh > 0 else { return nil }
        let f = cap.window.frame
        // Clipped to the window: nothing outside it can be clicked.
        let px = f.minX + min(max(Double(x), 0), Double(fw - 1)) / Double(fw) * f.width
        let py = f.minY + min(max(Double(y), 0), Double(fh - 1)) / Double(fh) * f.height
        return CGPoint(x: px, y: py)
    }

    mutating func handle(_ cap: Capture, kind: String, x: Int, y: Int, dy: Int) {
        guard AXIsProcessTrusted() else {
            sendEvent("error", code: "permission", msg: "Accessibility permission is missing for deckcap-mac (System Settings > Privacy & Security > Accessibility)")
            return
        }
        guard let p = point(cap, x, y) else { return }
        func post(_ t: CGEventType) {
            CGEvent(mouseEventSource: nil, mouseType: t, mouseCursorPosition: p, mouseButton: .left)?.post(tap: .cghidEventTap)
        }
        if kind == "down" || kind == "tap" {
            // The window may be behind others: events go to whatever is under the pointer.
            if let app = cap.window.owningApplication {
                NSRunningApplication(processIdentifier: app.processID)?.activate(options: [])
            }
        }
        switch kind {
        case "tap": post(.mouseMoved); post(.leftMouseDown); post(.leftMouseUp)
        case "down": post(.mouseMoved); post(.leftMouseDown); dragging = true
        case "move": post(dragging ? .leftMouseDragged : .mouseMoved)
        case "up": post(.leftMouseUp); dragging = false
        case "wheel":
            post(.mouseMoved)
            CGEvent(scrollWheelEvent2Source: nil, units: .pixel, wheelCount: 1, wheel1: Int32(-dy), wheel2: 0, wheel3: 0)?.post(tap: .cghidEventTap)
        default: sendEvent("error", code: "unsupported", msg: "input kind \(kind)")
        }
    }
}

// MARK: commands

struct Command: Decodable {
    var cmd: String
    var window: String?
    var fps: Int?
    var maxw: Int?
    var audio: Bool?
    var kind: String?
    var x: Int?
    var y: Int?
    var dy: Int?
}

func shareable() async -> SCShareableContent? {
    do {
        return try await SCShareableContent.excludingDesktopWindows(true, onScreenWindowsOnly: false)
    } catch {
        sendEvent("error", code: "permission", msg: "Screen Recording permission is missing for deckcap-mac (System Settings > Privacy & Security > Screen & System Audio Recording): \(error.localizedDescription)")
        return nil
    }
}

actor Helper {
    var cap: Capture?
    var input = Input()

    func handle(_ c: Command) async {
        switch c.cmd {
        case "list":
            guard let content = await shareable() else { return }
            let list: [[String: Any]] = content.windows
                .filter { w in
                    // On-screen app windows only: the system's overlays and helper windows are not
                    // something to put on a button.
                    guard w.windowLayer == 0, w.isOnScreen, w.frame.width > 100, w.frame.height > 80,
                          let app = w.owningApplication else { return false }
                    return !["WindowManager", "Window Server", "Dock", "Control Centre", "Control Center"].contains(app.applicationName)
                }
                .map { ["id": String($0.windowID), "title": $0.title ?? "",
                        "app": $0.owningApplication?.applicationName ?? "",
                        "w": Int($0.frame.width), "h": Int($0.frame.height)] }
            send(1, (try? JSONSerialization.data(withJSONObject: list)) ?? Data("[]".utf8))
        case "start":
            await cap?.stop(); cap = nil
            guard let content = await shareable() else { return }
            guard let id = UInt32(c.window ?? ""), let win = content.windows.first(where: { $0.windowID == id }) else {
                sendEvent("error", code: "no_window", msg: "window \(c.window ?? "") not found")
                return
            }
            let k = Capture(window: win)
            do {
                try await k.start(fps: c.fps ?? 25, maxW: c.maxw ?? 960, audio: c.audio ?? false)
                cap = k
                sendEvent("started", msg: c.window ?? "")
            } catch {
                sendEvent("error", code: "internal", msg: "start: \(error.localizedDescription)")
            }
        case "stop":
            await cap?.stop(); cap = nil
            input.dragging = false
            sendEvent("stopped")
        case "input":
            guard let k = cap else { return }
            input.handle(k, kind: c.kind ?? "", x: c.x ?? 0, y: c.y ?? 0, dy: c.dy ?? 0)
        default:
            sendEvent("error", code: "unsupported", msg: "command \(c.cmd)")
        }
    }
}

@main
struct Main {
    static func main() async {
        // A command-line tool has no app to connect CoreGraphics to the window server; SCStream
        // asserts it (CGS_REQUIRE_INIT). Asking for the main display makes the connection.
        _ = CGMainDisplayID()
        let helper = Helper()
        sendEvent("ready")
        // Commands arrive on stdin; reading blocks, so it gets its own thread. Handling is in order.
        let (stream, cont) = AsyncStream.makeStream(of: Command.self)
        Thread.detachNewThread {
            while let line = readLine(strippingNewline: true) {
                if let c = try? JSONDecoder().decode(Command.self, from: Data(line.utf8)) {
                    cont.yield(c)
                } else {
                    sendEvent("error", code: "unsupported", msg: "bad command line")
                }
            }
            cont.finish() // stdin closed: quit
        }
        for await c in stream { await helper.handle(c) }
    }
}
