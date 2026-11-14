#!/usr/bin/env python3
"""Generate a guarded Go source overlay, without modifying the NSGo module.

Missing upstream configuration hooks are kept here until the published fork
supports them. Every replacement must match exactly once; upstream drift fails
before a package is produced. All materialized files stay in ignored build/.
"""
import json
from pathlib import Path
import subprocess
import sys

root = Path(__file__).resolve().parents[2]
module = Path(subprocess.check_output(
    ['go', 'list', '-m', '-f', '{{.Dir}}', 'github.com/nsgo-dev/nsgo'], cwd=root, text=True).strip())
out = root / 'build/.nsgo-overlay'
out.mkdir(parents=True, exist_ok=True)
replacements = {}


def patch(file, changes):
    source = module / file
    text = Path(replacements.get(str(source), source)).read_text()
    for before, after in changes:
        if text.count(before) != 1:
            sys.exit(f'NSGo packaging overlay is incompatible with {source}: expected one {before!r}; update the bridge or pin the documented fork')
        text = text.replace(before, after, 1)
    target = out / file
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(text)
    replacements[str(source)] = str(target)


patch('cmd/nsgo/project.go', [
    ('\tName    string', '\tMiya miyaPackaging `yaml:"miya"`\n\tMiyaTarget string `yaml:"-"`\n\tName    string'),
    ('\tfor _, e := range extra {', '\tflags += " " + strings.ReplaceAll(p.Miya.LDFlags, "{version}", p.Version)\n\tfor _, e := range extra {'),
    ('\tif p.BuildNumber > 0 {', '\tif strings.HasPrefix(p.MiyaTarget, "macos_") && p.Miya.MacOS.BuildNumber == "version" { return p.Version }\n\tif p.BuildNumber > 0 {'),
])
patch('cmd/nsgo/build.go', [
    ('\t"macos_arm64", "macos_x64",', '\t"macos_arm64", "macos_x64", "macos_universal",'),
    ('func buildTarget(ctx context.Context, p *project, target, dir string) error {', '''func buildTarget(ctx context.Context, p *project, target, dir string) error {
    p.MiyaTarget = target
    if target == "ios" && p.Build.team == "" { p.Build.team = p.Miya.IOS.DevelopmentTeam }
    if strings.HasPrefix(target, "macos_") && p.Sign.Identity == "" { p.Sign.Identity = "-" }
    if strings.HasPrefix(target, "ios") && p.Miya.IOS.Icon != "" {
        p.Icon.Apple = ""
        p.Icon.PNG = p.resolve(p.Miya.IOS.Icon)
    }'''),
    ('case "macos_arm64", "macos_x64":\n\t\tapp, err := buildMacApp(ctx, p, dir, goArch(arch))', '''case "macos_arm64", "macos_x64", "macos_universal":
        var app string
        var err error
        if arch == "universal" { app, err = buildMiyaUniversal(ctx, p, dir) } else { app, err = buildMacApp(ctx, p, dir, goArch(arch)) }'''),
    ('\ttags := extra.tags', '\ttags := append(append([]string(nil), extra.tags...), p.Miya.Tags...)'),
    ('\treturn os.WriteFile(path, []byte(plist), 0o644)', '''    data, err := miyaPlist(p, []byte(plist), t == macPlist)
    if err != nil { return err }
    return os.WriteFile(path, data, 0o644)'''),
])
patch('cmd/nsgo/widgets.go', [
    ('cache, err := os.UserCacheDir()', 'cache, err := miyaBuildCacheDir()'),
])
patch('cmd/nsgo/icon.go', [
    ('cache, err := os.UserCacheDir()', 'cache, err := miyaBuildCacheDir()'),
])
patch('cmd/nsgo/natives.go', [
    ('args := []string{"list", "-deps",', 'args := []string{"list", "-tags", strings.Join(p.Miya.Tags, ","), "-deps",'),
])
patch('cmd/nsgo/resources.go', [
    ('\tsrc := filepath.Join(p.Dir, resourcesDir)', '\tif len(p.Miya.Resources) > 0 { return miyaResources(p, f) }\n\tsrc := filepath.Join(p.Dir, resourcesDir)'),
])
patch('cmd/nsgo/sign.go', [
    ('if ents := p.hardenedEntitlements(); p.notarizing() && len(ents) > 0 {', 'if ents := p.hardenedEntitlements(); len(ents) > 0 {'),
    ('\tents := map[string]any{}', '\tents := map[string]any{}\n\tfor k, v := range p.Miya.MacOS.Entitlements { ents[k] = v }'),
    ('\t// Devices refuse an app', '\tif err := miyaIOSEntitlements(p, entitlements, true); err != nil { return err }\n\t// Devices refuse an app'),
])
patch('cmd/nsgo/notarize.go', [
    ('return []string{"--keychain-profile", p.Sign.Notarize}, nil', '''args := []string{"--keychain-profile", p.Sign.Notarize}
    if p.Miya.NotaryKeychain != "" { args=append(args,"--keychain",p.Miya.NotaryKeychain) }
    return args,nil'''),
])
patch('cmd/nsgo/phone.go', [
    ('\treturn ents\n}', '\tif err := miyaIOSEntitlements(p, ents, false); err != nil { panic(err) }\n\treturn ents\n}'),
])
# Feed the app's extra manifest properties to NSGo's existing binary XML writer.
patch('cmd/nsgo/android.go', [
    ('\t\tPackage:                p.ID,', '''        URLSchemes: p.Miya.URLSchemes,
        MinimumSDK: p.Miya.Android.MinimumSDK,
        PermissionMaxSDK: p.Miya.Android.PermissionMaxSDK,
        ServiceSubtype: p.Miya.Android.ForegroundServiceSubtype,
        Package:                p.ID,'''),
])
patch('cmd/nsgo/apk/axml.go', [
    ('var androidAttrs = map[string]attrSpec{', '''var androidAttrs = map[string]attrSpec{
    "scheme": {0x01010027, typeString, nil},
    "value": {0x01010024, typeString, nil},
    "maxSdkVersion": {0x01010271, typeInt, parseInt},'''),
    ('type Manifest struct {', '''type Manifest struct {
    URLSchemes []string
    MinimumSDK int
    PermissionMaxSDK map[string]int
    ServiceSubtype string'''),
    ('\tif len(m.ForegroundServiceTypes) > 0 {', '''    if m.MinimumSDK > 0 { setAttr(child(root, "uses-sdk"), androidNS, "minSdkVersion", strconv.Itoa(m.MinimumSDK)) }
    for _, scheme := range m.URLSchemes {
        intent := &node{name: xml.Name{Local: "intent-filter"}}
        for _, pair := range [][2]string{{"action", "android.intent.action.VIEW"}, {"category", "android.intent.category.DEFAULT"}, {"category", "android.intent.category.BROWSABLE"}} {
            item := &node{name: xml.Name{Local: pair[0]}}
            setAttr(item, androidNS, "name", pair[1]); intent.kids = append(intent.kids, item)
        }
        data := &node{name: xml.Name{Local: "data"}}
        setAttr(data, androidNS, "scheme", scheme); intent.kids = append(intent.kids, data)
        activity := child(app, "activity"); activity.kids = append(activity.kids, intent)
    }
    if len(m.ForegroundServiceTypes) > 0 {'''),
    ('\t\tapp.kids = append(app.kids, service)', '''        if m.ServiceSubtype != "" {
            property := &node{name: xml.Name{Local: "property"}}
            setAttr(property, androidNS, "name", "android.app.PROPERTY_SPECIAL_USE_FGS_SUBTYPE")
            setAttr(property, androidNS, "value", m.ServiceSubtype)
            service.kids = append(service.kids, property)
        }
        app.kids = append(app.kids, service)'''),
    ('\tif m.TargetSDK > 0 {', '''    for _, use := range root.kids {
        if use.name.Local != "uses-permission" { continue }
        for _, a := range use.attrs {
            if a.Name.Local == "name" && m.PermissionMaxSDK[a.Value] > 0 {
                setAttr(use, androidNS, "maxSdkVersion", strconv.Itoa(m.PermissionMaxSDK[a.Value]))
            }
        }
    }
    if m.TargetSDK > 0 {'''),
    ('"microphone": 128}', '"microphone": 128, "specialUse": 0x40000000}'),
])
# Preserve MyGo's application policy as well as its permissions. The network
# resource uses the same binary XML/resource-table encoder, with text chunks.
patch('cmd/nsgo/android.go', [
    ('        URLSchemes: p.Miya.URLSchemes,', '''        AppAttributes: p.Miya.Android.AppAttributes,
        ActivityAttributes: p.Miya.Android.ActivityAttributes,
        ExtraConfigChanges: p.Miya.Android.ExtraConfigChanges,
        OptionalFeatures: p.Miya.Android.OptionalFeatures,
        Queries: p.Miya.Android.Queries,
        NetworkSecurityXML: []byte(p.Miya.Android.NetworkSecurityXML),
        URLSchemes: p.Miya.URLSchemes,'''),
    ('return buildAndroidPackage(ctx, p, dir, goarch, ".aab", apk.WriteBundle)', 'return "", fmt.Errorf("Miya ships APKs; the network-security resource bridge does not support AABs yet")'),
])
patch('cmd/nsgo/apk/axml.go', [
    ('type Manifest struct {', '''type Manifest struct {
    AppAttributes map[string]any
    ActivityAttributes map[string]any
    ExtraConfigChanges []string
    OptionalFeatures []string
    Queries []IntentQuery
    NetworkSecurityXML []byte'''),
    ('var androidAttrs = map[string]attrSpec{', '''var androidAttrs = map[string]attrSpec{
    "allowBackup": {0x01010280,typeBool,parseBool},
    "supportsRtl": {0x010103af,typeBool,parseBool},
    "launchMode": {0x0101001d,typeInt,parseMiyaLaunchMode},
    "required": {0x0101028e,typeBool,parseBool},
    "mimeType": {0x01010026,typeString,nil},
    "networkSecurityConfig": {0x01010527,typeReference,parseAppResource},'''),
    ('var configChanges = map[string]uint32{', 'var configChanges = map[string]uint32{\n    "fontWeightAdjustment": 0x10000000,'),
    ('\te := &encoder{index: map[string]int{}, icon: m.Icon != nil}', '''    return encodeMiyaXML(root,m)
}
func encodeMiyaXML(root *node,m Manifest)([]byte,error){
    e := &encoder{index: map[string]int{}, icon: m.Icon != nil, miyaNetworkID:miyaNetworkID(m)}'''),
    ('\tif len(m.Names) > 0 {', '\tmiyaManifest(root,app,m)\n\tif len(m.Names) > 0 {'),
    ('type encoder struct {', 'type encoder struct {\n    miyaNetworkID uint32'),
    ('\t\t\tif spec.kind == typeString {', '''            if a.Name.Local == "networkSecurityConfig" { attr.kind,attr.data=typeReference,e.miyaNetworkID;break }
            if spec.kind == typeString {'''),
    ('\tfor _, kid := range n.kids {\n\t\tif err := e.element(kid);', '''    if text:=strings.TrimSpace(n.text);text!="" {
        index:=e.str(text)
        w(uint16(0x0104));w(uint16(16));w(uint32(28));w(uint32(0));w(uint32(noIndex))
        w(index);w(uint16(8));w(uint8(0));w(uint8(typeString));w(index)
    }
    for _, kid := range n.kids {
        if err := e.element(kid);'''),
    ('case "":\n\t\t\tattr.name = e.str(a.Name.Local)\n\t\t\tattr.kind = typeString\n\t\t\tattr.data = e.str(a.Value)\n\t\t\tattr.raw = int32(attr.data)', '''case "":
            attr.name=e.str(a.Name.Local);attr.kind=typeString;attr.data=e.str(a.Value);attr.raw=int32(attr.data)
            if a.Name.Local=="cleartextTrafficPermitted" || a.Name.Local=="includeSubdomains" {
                value,err:=parseBool(a.Value);if err!=nil{return err}
                attr.kind=typeBool;attr.data=value;attr.raw=-1
            }'''),
    ('type node struct {', 'type node struct {\n    text string'),
    ('\t\tcase xml.EndElement:', '''        case xml.CharData:
            if len(stack)>0 { stack[len(stack)-1].text+=string(t) }
        case xml.EndElement:'''),
])
patch('cmd/nsgo/apk/resources.go', [
    ('\tvar types []resType', '''    return table(pkg, miyaResourceTypes(icon,names,false))
}
func miyaResourceTypes(icon bool,names map[string]string,network bool)[]resType{
    var types []resType'''),
    ('\treturn table(pkg, types)', '''    if network { types=append(types,resType{name:"xml",key:"miya_network_security",values:[]resValue{{newConfig(),"res/xml/miya_network_security.xml"}}}) }
    return types'''),
])
patch('cmd/nsgo/apk/apk.go', [
    ('\tzip, err := archive(all)', '''    if len(m.NetworkSecurityXML)>0 {
        data,err:=miyaNetworkXML(m.NetworkSecurityXML);if err!=nil{return err}
        all=append(all,File{Name:"res/xml/miya_network_security.xml",Data:data,Stored:true})
        for i:=range all {if all[i].Name=="resources.arsc" {all[i].Data=miyaAPKResources(m.Package,m.Icon!=nil,m.Names,true)}}
    }
    zip, err := archive(all)'''),
])
replacements[str(module / 'cmd/nsgo/apk/miya_packaging.go')] = str(root / 'scripts/packaging/nsgo-apk-compat.go.txt')
# New source files are visible to go list/build through the overlay as well.
replacements[str(module / 'cmd/nsgo/miya_packaging.go')] = str(root / 'scripts/packaging/nsgo-compat.go.txt')
replacements[str(module / 'cmd/nsgo/miya_packaging_test.go')] = str(root / 'scripts/packaging/nsgo-compat_test.go.txt')
path = out / 'overlay.json'
path.write_text(json.dumps({'Replace': replacements}, indent=2) + '\n')
print(path)
