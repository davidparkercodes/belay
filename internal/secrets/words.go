package secrets

import "strings"

const commonWordList = `
able ably acid aged ages aide aids aims airs ally also alto amid area arms army arts asks atom aunt
auto avid away awry axes axis baby back bags bait bake bald ball band bank bare bark barn base bass
bath bead beam bean bear beat beds beef been beer bees bell belt bend bent best beta bias bike bill
bind bird bite bits blew blob blog blow blue blur boat body boil bold bolt bomb bond bone book boom
boot bore born boss both bout bowl boys brew brow buck bugs bulb bulk bull bump burn bury bush busy
buys buzz byte cafe cage cake call calm came camp cans cape card care cars cart case cash cast cats
cave cell cent chat chef chin chip city clad clan clap clay clip club clue coal coat code coin cold
come cone cook cool cope copy cord core cork corn cost cozy crew crop crow cube cult cups curb cure
curl cute cuts dads dame damp dare dark dart dash data date dawn days dead deaf deal dean dear debt
deck deed deem deep deer demo deny desk dial dice diet digs dirt disc dish disk dive dock does dogs
doll dome done doom door dose dots dove down drag draw drew drip drop drug drum dual duck dude duel
dues dull dumb dump dune dust duty each earn ears ease east easy eats echo edge edit eggs else emit
ends envy epic even ever evil exam exit eyes face fact fade fail fair fake fall fame fans fare farm
fast fate fear feat feed feel fees feet fell felt fern file fill film find fine fire firm fish fist
fits five flag flat fled flew flip flow foam foil fold folk fond font food fool foot ford fork form
fort foul four free frog from fuel full fund fury fuse fuss gain gale game gang gaps gate gave gaze
gear gems gene gift girl give glad glow glue goal goat goes gold golf gone good grab gray grew grey
grid grim grin grip grow gulf guns guru guts guys hack hair half hall halt hand hang hard harm hash
hate haul have hawk head heal heap hear heat heel held hell helm help herb herd here hero hers hide
high hike hill hint hire hits hold hole holy home hood hook hope horn hose host hour huge hull hung
hunt hurt hush icon idea idle inch info into iron isle item jail jars jazz jean jets jobs join joke
jump june jury just keen keep kept keys kick kids kill kind king kiss kite knee knew knit knot know
labs lack lady laid lake lamb lamp land lane last late lawn laws lazy lead leaf leak lean leap left
legs lend lens less lest lets liar lick lied lies life lift like limb lime limp line link lion lips
list live load loan lock logo logs lone long look loop lord lose loss lost lots loud love luck lump
lung lure lush made mail main make male mall many maps mark mask mass mate math maze meal mean meat
meet melt memo menu mere mesh mess mice mild mile milk mill mind mine mint miss mist mode mold mood
moon more moss most moth move much mule must myth nail name navy near neat neck need nest nets news
next nice nine node none noon norm nose note noun nuts oath obey odds oils okay once ones only onto
open oral ours oust outs oven over owed owes owns pace pack pact page paid pain pair pale palm pane
park part pass past path peak pear peel peer pens pick pier pile pill pine pink pipe plan play plea
plot plug plus poem poet pole poll pond pony pool poor pops pork port pose post pour pray prep prey
prod prop pros pull pump punk pure push puts quit quiz race rack rage raid rail rain rank rare rate
rays read real rear reed reef reel rely rent rest rice rich ride rift ring riot rise risk road roar
robe rock rode role roll roof room root rope rose ruin rule rush rust safe saga said sail sake sale
salt same sand sang save says scan seal seam seat sees seed seek seem seen self sell send sent sets
shed ship shoe shop shot show shut sick side sigh sign silk sing sink site size skin skip slab slam
slid slim slip slot slow snap snow soak soap soar sock soft soil sold sole some song soon sort soul
soup sour span spin spot spun star stay stem step stir stop such suit sums sung sunk sure surf swap
swim tabs tack tail take tale talk tall tank tape taps task taxi team tear tech teen tell temp tend
tens tent term test text than that them then they thin this thus tick tide tidy tied tier ties tile
till time tiny tips tire told toll tone tons took tool tops tore torn toss tour town toys tray tree
trek trim trio trip true tube tuck tune turn twin type unit upon urge used user uses vain vary vast
verb very vest veto vice view vine visa void vote wage wait wake walk wall want ward warm warn wars
wash wave ways weak wear webs week well went were west what when whom wide wife wild will wind wine
wing wipe wire wise wish with woke wolf wood wool word wore work worm worn wrap yard yarn yeah year
yoga your yours zero zone zoom
args auth bool char conf cron diff dirs docs enum exec func goto heap href http html imap init json
kube ldap lint mock mute nano null opts oidc pids ping repo repl ruby saml sass smtp sqrt stub sudo
sync tags todo totp hotp hmac tmux uuid vars wasm yaml toml grep curl wget bash cors csrf jwks wifi
sftp gzip cert keys perl java rest crud blob hook pull tree tags main slug
also does gets goes hers ours mine whom were else thee upon unto onto amid
`

var commonWords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(commonWordList) {
		m[w] = true
	}
	return m
}()

func commonWord(lw []byte) bool {
	return commonWords[string(lw)]
}
