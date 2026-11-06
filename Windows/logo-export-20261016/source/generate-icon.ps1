$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Drawing
Add-Type -TypeDefinition 'using System; using System.Runtime.InteropServices; public static class IconHandle { [DllImport("user32.dll")] public static extern bool DestroyIcon(IntPtr handle); }'
$bitmap = [System.Drawing.Bitmap]::new(64,64)
$graphics = [System.Drawing.Graphics]::FromImage($bitmap)
$graphics.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::AntiAlias
$green = [System.Drawing.ColorTranslator]::FromHtml('#23846c')
$graphics.Clear($green)
$leaf = [System.Drawing.Drawing2D.GraphicsPath]::new()
$leaf.AddBezier(32,51,18,47,12,34,13,21)
$leaf.AddBezier(13,21,25,23,33,34,32,51)
$brush = [System.Drawing.SolidBrush]::new([System.Drawing.ColorTranslator]::FromHtml('#d4f5dc'))
$graphics.FillPath($brush,$leaf)
$leaf.Reset()
$leaf.AddBezier(33,51,30,32,35,16,51,10)
$leaf.AddBezier(51,10,53,28,45,44,33,51)
$graphics.FillPath([System.Drawing.Brushes]::White,$leaf)
$pen = [System.Drawing.Pen]::new($green,2.5)
$graphics.DrawLine($pen,31,51,22,33)
$graphics.DrawLine($pen,33,50,43,24)
$iconPointer = $bitmap.GetHicon()
$icon = [System.Drawing.Icon]::FromHandle($iconPointer)
$target = Join-Path (Split-Path $PSScriptRoot -Parent) 'app\windows\runner\resources\app_icon.ico'
$stream = [System.IO.File]::Create($target)
try { $icon.Save($stream) } finally {
    $stream.Dispose(); $icon.Dispose(); [IconHandle]::DestroyIcon($iconPointer) | Out-Null
    $pen.Dispose(); $brush.Dispose(); $leaf.Dispose(); $graphics.Dispose(); $bitmap.Dispose()
}
