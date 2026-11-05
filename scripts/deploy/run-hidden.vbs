Option Explicit

Dim shell, command, index
Set shell = CreateObject("WScript.Shell")
command = "powershell.exe"

For index = 0 To WScript.Arguments.Count - 1
    command = command & " " & QuoteArgument(WScript.Arguments(index))
Next

shell.Run command, 0, False

Function QuoteArgument(value)
    QuoteArgument = Chr(34) & Replace(CStr(value), Chr(34), Chr(34) & Chr(34)) & Chr(34)
End Function
