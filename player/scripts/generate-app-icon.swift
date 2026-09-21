#!/usr/bin/env swift

import AppKit
import Foundation

private let root = URL(fileURLWithPath: #filePath)
    .deletingLastPathComponent()
    .deletingLastPathComponent()
private let output = root.appendingPathComponent("Resources/AppIcon.icns")
private let iconset = FileManager.default.temporaryDirectory
    .appendingPathComponent("lilt-AppIcon-\(UUID().uuidString).iconset")

private struct IconVariant {
    let points: Int
    let scale: Int

    var pixels: Int { points * scale }
    var filename: String {
        scale == 1 ? "icon_\(points)x\(points).png" : "icon_\(points)x\(points)@2x.png"
    }
}

private let variants = [
    IconVariant(points: 16, scale: 1), IconVariant(points: 16, scale: 2),
    IconVariant(points: 32, scale: 1), IconVariant(points: 32, scale: 2),
    IconVariant(points: 128, scale: 1), IconVariant(points: 128, scale: 2),
    IconVariant(points: 256, scale: 1), IconVariant(points: 256, scale: 2),
    IconVariant(points: 512, scale: 1), IconVariant(points: 512, scale: 2),
]

private func renderIcon(pixels: Int) throws -> Data {
    let size = NSSize(width: pixels, height: pixels)
    guard let bitmap = NSBitmapImageRep(
        bitmapDataPlanes: nil,
        pixelsWide: pixels,
        pixelsHigh: pixels,
        bitsPerSample: 8,
        samplesPerPixel: 4,
        hasAlpha: true,
        isPlanar: false,
        colorSpaceName: .deviceRGB,
        bytesPerRow: 0,
        bitsPerPixel: 0
    ), let context = NSGraphicsContext(bitmapImageRep: bitmap) else {
        throw NSError(domain: "lilt.icon", code: 1, userInfo: [NSLocalizedDescriptionKey: "no bitmap context"])
    }
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = context
    defer { NSGraphicsContext.restoreGraphicsState() }
    context.imageInterpolation = .high
    NSColor.clear.setFill()
    NSRect(origin: .zero, size: size).fill()

    let unit = CGFloat(pixels) / 1024
    let tileRect = NSRect(x: 64 * unit, y: 64 * unit, width: 896 * unit, height: 896 * unit)
    let tile = NSBezierPath(roundedRect: tileRect, xRadius: 205 * unit, yRadius: 205 * unit)
    let background = NSGradient(colors: [
        NSColor(calibratedRed: 0.10, green: 0.12, blue: 0.16, alpha: 1),
        NSColor(calibratedRed: 0.16, green: 0.20, blue: 0.27, alpha: 1),
    ])!
    background.draw(in: tile, angle: 90)

    NSColor(calibratedWhite: 1, alpha: 0.10).setStroke()
    tile.lineWidth = max(1, 5 * unit)
    tile.stroke()

    // Seven rounded bars form a compact audio waveform. Drawing from normalized
    // geometry keeps the 16 px icon recognizable without a separate design.
    let heights: [CGFloat] = [230, 390, 590, 720, 590, 390, 230]
    let barWidth: CGFloat = 72
    let gap: CGFloat = 42
    let totalWidth = CGFloat(heights.count) * barWidth + CGFloat(heights.count - 1) * gap
    let startX = (1024 - totalWidth) / 2
    let waveform = NSGradient(colors: [
        NSColor(calibratedRed: 0.73, green: 0.96, blue: 0.88, alpha: 1),
        NSColor(calibratedRed: 0.46, green: 0.82, blue: 0.98, alpha: 1),
    ])!
    for (index, height) in heights.enumerated() {
        let rect = NSRect(
            x: (startX + CGFloat(index) * (barWidth + gap)) * unit,
            y: (512 - height / 2) * unit,
            width: barWidth * unit,
            height: height * unit
        )
        let bar = NSBezierPath(roundedRect: rect, xRadius: 36 * unit, yRadius: 36 * unit)
        waveform.draw(in: bar, angle: 90)
    }

    context.flushGraphics()
    guard let png = bitmap.representation(using: .png, properties: [:]) else {
        throw NSError(domain: "lilt.icon", code: 2, userInfo: [NSLocalizedDescriptionKey: "PNG encoding failed"])
    }
    return png
}

try FileManager.default.createDirectory(at: iconset, withIntermediateDirectories: true)
defer { try? FileManager.default.removeItem(at: iconset) }
for variant in variants {
    let data = try renderIcon(pixels: variant.pixels)
    try data.write(to: iconset.appendingPathComponent(variant.filename), options: .atomic)
}

let iconutil = Process()
iconutil.executableURL = URL(fileURLWithPath: "/usr/bin/iconutil")
iconutil.arguments = ["-c", "icns", iconset.path, "-o", output.path]
try iconutil.run()
iconutil.waitUntilExit()
guard iconutil.terminationStatus == 0 else {
    throw NSError(domain: "lilt.icon", code: Int(iconutil.terminationStatus), userInfo: [NSLocalizedDescriptionKey: "iconutil failed"])
}
print(output.path)
