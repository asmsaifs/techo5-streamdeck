// Spike 0.5.3: can ScreenCaptureKit capture one window, and its app's sound, fast enough to stream?
// It lists the windows, picks the first whose app or title contains the argument (or the largest
// on-screen window), captures it at up to 30 fps at most 960 wide with the app's audio for a few
// seconds, and prints frames and audio per second.
//
//	swiftc -O -o bin/sckit spikes/sckit/main.swift && bin/sckit Safari
//
// Needs Screen Recording permission for whatever runs it (System Settings > Privacy & Security).

import AVFoundation
import CoreMedia
import Foundation
import ScreenCaptureKit

final class Counter: NSObject, SCStreamOutput, SCStreamDelegate {
    let lock = NSLock()
    var frames = 0, idle = 0, audioFrames = 0, audioBuffers = 0
    var size = CGSize.zero
    var audioFormat = ""

    func stream(_ stream: SCStream, didOutputSampleBuffer sb: CMSampleBuffer, of type: SCStreamOutputType) {
        lock.lock(); defer { lock.unlock() }
        switch type {
        case .screen:
            // A frame with status other than complete is the window not changing: SCK says so rather
            // than sending the same picture again, which is what a deck wants.
            guard let att = CMSampleBufferGetSampleAttachmentsArray(sb, createIfNecessary: false) as? [[SCStreamFrameInfo: Any]],
                  let raw = att.first?[.status] as? Int, let status = SCFrameStatus(rawValue: raw) else { return }
            if status == .complete {
                frames += 1
                if let img = sb.imageBuffer {
                    size = CGSize(width: CVPixelBufferGetWidth(img), height: CVPixelBufferGetHeight(img))
                }
            } else {
                idle += 1
            }
        case .audio:
            audioBuffers += 1
            audioFrames += CMSampleBufferGetNumSamples(sb)
            if audioFormat.isEmpty, let fd = sb.formatDescription,
               let asbd = CMAudioFormatDescriptionGetStreamBasicDescription(fd)?.pointee {
                audioFormat = "\(Int(asbd.mSampleRate)) Hz, \(asbd.mChannelsPerFrame) ch, \(asbd.mBitsPerChannel) bit, flags \(asbd.mFormatFlags)"
            }
        default:
            break
        }
    }

    func stream(_ stream: SCStream, didStopWithError error: Error) {
        print("stopped:", error)
    }

    func take() -> (Int, Int, Int, Int, CGSize) {
        lock.lock(); defer { lock.unlock() }
        let r = (frames, idle, audioFrames, audioBuffers, size)
        frames = 0; idle = 0; audioFrames = 0; audioBuffers = 0
        return r
    }
}

@main
struct Main {
    static func main() async {
        // A command-line tool has no app to set up CoreGraphics' connection to the window server,
        // and SCStream asserts it is there (CGS_REQUIRE_INIT). Asking for the main display makes it.
        _ = CGMainDisplayID()
        let want = CommandLine.arguments.dropFirst().first?.lowercased()
        let content: SCShareableContent
        do {
            content = try await SCShareableContent.excludingDesktopWindows(true, onScreenWindowsOnly: false)
        } catch {
            print("no shareable content (Screen Recording permission?):", error)
            exit(1)
        }
        let windows = content.windows.filter { $0.windowLayer == 0 && $0.frame.width > 200 && $0.frame.height > 150 }
        print("\(windows.count) windows:")
        for w in windows.prefix(15) {
            print(String(format: "  %6d  %-24@ %-40@ %4.0fx%-4.0f %@", w.windowID,
                         (w.owningApplication?.applicationName ?? "?") as NSString,
                         String((w.title ?? "").prefix(40)) as NSString,
                         w.frame.width, w.frame.height, w.isOnScreen ? "on" : "off"))
        }
        let pick: SCWindow?
        if let want {
            pick = windows.first {
                ($0.owningApplication?.applicationName.lowercased().contains(want) ?? false)
                    || ($0.title?.lowercased().contains(want) ?? false)
            }
        } else {
            pick = windows.filter { $0.isOnScreen }.max { $0.frame.width * $0.frame.height < $1.frame.width * $1.frame.height }
        }
        guard let win = pick else {
            print("no window matches")
            exit(1)
        }
        print("capturing \(win.windowID) \(win.owningApplication?.applicationName ?? "?") \"\(win.title ?? "")\"")

        // A window-only filter: the window as it is, even when covered by others.
        let filter = SCContentFilter(desktopIndependentWindow: win)
        let cfg = SCStreamConfiguration()
        let scale = min(1, 960 / win.frame.width)
        cfg.width = Int(win.frame.width * scale)
        cfg.height = Int(win.frame.height * scale)
        cfg.minimumFrameInterval = CMTime(value: 1, timescale: 30)
        cfg.pixelFormat = kCVPixelFormatType_32BGRA
        cfg.showsCursor = false
        cfg.queueDepth = 5
        // The app's sound only: a window filter limits audio to the window's app.
        cfg.capturesAudio = true
        cfg.sampleRate = 48000
        cfg.channelCount = 2
        cfg.excludesCurrentProcessAudio = true

        let counter = Counter()
        let stream = SCStream(filter: filter, configuration: cfg, delegate: counter)
        let q = DispatchQueue(label: "sckit")
        do {
            try stream.addStreamOutput(counter, type: .screen, sampleHandlerQueue: q)
            try stream.addStreamOutput(counter, type: .audio, sampleHandlerQueue: q)
            try await stream.startCapture()
        } catch {
            print("capture failed:", error)
            exit(1)
        }
        for s in 1...5 {
            try? await Task.sleep(nanoseconds: 1_000_000_000)
            let (f, idle, af, ab, size) = counter.take()
            print("second \(s): \(f) frames (\(idle) unchanged) at \(Int(size.width))x\(Int(size.height)), audio \(af) samples in \(ab) buffers")
        }
        print("audio format:", counter.audioFormat.isEmpty ? "none received" : counter.audioFormat)
        try? await stream.stopCapture()
    }
}
