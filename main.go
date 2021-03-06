package main

import (
	"TurtleCoin-Nest/turtlecoinwalletdrpcgo"
	"TurtleCoin-Nest/walletdmanager"
	"database/sql"
	"encoding/json"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"time"

	"github.com/atotto/clipboard"
	"github.com/dustin/go-humanize"
	_ "github.com/mattn/go-sqlite3"
	log "github.com/sirupsen/logrus"
	"github.com/therecipe/qt/core"
	"github.com/therecipe/qt/gui"
	"github.com/therecipe/qt/qml"
	"github.com/therecipe/qt/quickcontrols2"
)

var (
	// qmlObjects = make(map[string]*core.QObject)
	qmlBridge                   *QmlBridge
	transfers                   []turtlecoinwalletdrpcgo.Transfer
	tickerRefreshWalletData     *time.Ticker
	tickerRefreshConnectionInfo *time.Ticker
	db                          *sql.DB
	useRemoteNode               = true
	displayFiatConversion       = false
	stringBackupKeys            = ""
	rateUSDTRTL                 float64 // USD value for 1 TRTL
	remoteDaemonAddress         = defaultRemoteDaemonAddress
	remoteDaemonPort            = defaultRemoteDaemonPort
	limitDisplayedTransactions  = true
	countConnectionProblem      = 0
)

// QmlBridge is the bridge between qml and go
type QmlBridge struct {
	core.QObject

	// go to qml
	_ func(balance string,
		balanceUSD string) `signal:"displayTotalBalance"`
	_ func(data string) `signal:"displayAvailableBalance"`
	_ func(data string) `signal:"displayLockedBalance"`
	_ func(address string,
		wallet string,
		displayFiatConversion bool) `signal:"displayAddress"`
	_ func(paymentID string,
		transactionID string,
		amount string,
		confirmations string,
		time string,
		number string) `signal:"addTransactionToList"`
	_ func(text string, time int)                       `signal:"displayPopup"`
	_ func(syncing string, blocks string, peers string) `signal:"displaySyncingInfo"`
	_ func(errorText string,
		errorInformativeText string) `signal:"displayErrorDialog"`
	_ func() `signal:"clearTransferAmount"`
	_ func() `signal:"clearListTransactions"`
	_ func(filename string,
		privateViewKey string,
		privateSpendKey string,
		walletAddress string) `signal:"displayPrivateKeys"`
	_ func()                            `signal:"displayOpenWalletScreen"`
	_ func()                            `signal:"displayMainWalletScreen"`
	_ func()                            `signal:"finishedLoadingWalletd"`
	_ func()                            `signal:"finishedCreatingWallet"`
	_ func(pathToPreviousWallet string) `signal:"displayPathToPreviousWallet"`
	_ func(walletLocation string)       `signal:"displayWalletCreationLocation"`
	_ func(useRemote bool,
		remoteNodeDescr string) `signal:"displayUseRemoteNode"`
	_ func()                 `signal:"hideSettingsScreen"`
	_ func()                 `signal:"displaySettingsScreen"`
	_ func(displayFiat bool) `signal:"displaySettingsValues"`
	_ func(remoteNodeAddress string,
		remoteNodePort string) `signal:"displaySettingsRemoteDaemonInfo"`
	_ func(fullBalance string)              `signal:"displayFullBalanceInTransferAmount"`
	_ func(fee string, mixin string)        `signal:"displayDefaultFeeAndMixin"`
	_ func(index int, confirmations string) `signal:"updateConfirmationsOfTransaction"`

	// qml to go
	_ func(msg string)           `slot:"log"`
	_ func(transactionID string) `slot:"clickedButtonExplorer"`
	_ func(transactionID string) `slot:"clickedButtonCopyTx"`
	_ func()                     `slot:"clickedButtonCopyAddress"`
	_ func()                     `slot:"clickedButtonCopyKeys"`
	_ func(transferAddress string,
		transferAmount string,
		transferPaymentID string,
		transferFee string,
		transferMixin string) `slot:"clickedButtonSend"`
	_ func()                                           `slot:"clickedButtonBackupWallet"`
	_ func()                                           `slot:"clickedCloseWallet"`
	_ func(pathToWallet string, passwordWallet string) `slot:"clickedButtonOpen"`
	_ func(filenameWallet string,
		passwordWallet string,
		confirmPasswordWallet string) `slot:"clickedButtonCreate"`
	_ func(filenameWallet string,
		passwordWallet string,
		privateViewKey string,
		privateSpendKey string,
		confirmPasswordWallet string) `slot:"clickedButtonImport"`
	_ func(remote bool)              `slot:"choseRemote"`
	_ func(amountTRTL string) string `slot:"getTransferAmountUSD"`
	_ func()                         `slot:"clickedCloseSettings"`
	_ func()                         `slot:"clickedSettingsButton"`
	_ func(displayFiat bool)         `slot:"choseDisplayFiat"`
	_ func(daemonAddress string,
		daemonPort string) `slot:"saveRemoteDaemonInfo"`
	_ func()                   `slot:"resetRemoteDaemonInfo"`
	_ func(transferFee string) `slot:"getFullBalanceAndDisplayInTransferAmount"`
	_ func()                   `slot:"getDefaultFeeAndMixinAndDisplay"`
	_ func(limit bool)         `slot:"limitDisplayTransactions"`

	_ func(object *core.QObject) `slot:"registerToGo"`
	_ func(objectName string)    `slot:"deregisterToGo"`
}

func main() {

	pathToLogFile := logFileFilename
	pathToDB := dbFilename
	pathToHomeDir := ""
	pathToAppDirectory, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		log.Fatal("error finding current directory. Error: ", err)
	}

	if isPlatformDarwin {
		usr, err := user.Current()
		if err != nil {
			log.Fatal(err)
		}
		pathToHomeDir = usr.HomeDir
		pathToAppFolder := pathToHomeDir + "/Library/Application Support/TurtleCoin-Nest"
		os.Mkdir(pathToAppFolder, os.ModePerm)
		pathToLogFile = pathToAppFolder + "/" + logFileFilename
		pathToDB = pathToAppFolder + "/" + pathToDB
	} else if isPlatformLinux {
		pathToLogFile = pathToAppDirectory + "/" + logFileFilename
		pathToDB = pathToAppDirectory + "/" + pathToDB
	}

	logFile, err := os.OpenFile(pathToLogFile, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		log.Fatal("error opening log file: ", err)
	}
	defer logFile.Close()

	if isPlatformLinux {
		// log to file and console
		mw := io.MultiWriter(os.Stdout, logFile)
		log.SetOutput(mw)
	} else {
		log.SetOutput(logFile)
	}

	log.SetLevel(log.DebugLevel)

	setupDB(pathToDB)

	log.WithField("version", versionNest).Info("Application started")

	go func() {
		requestRateTRTL()
	}()

	platform := "linux"
	if isPlatformDarwin {
		platform = "darwin"
	} else if isPlatformWindows {
		platform = "windows"
	}
	walletdmanager.Setup(platform)

	app := gui.NewQGuiApplication(len(os.Args), os.Args)
	app.SetWindowIcon(gui.NewQIcon5("qrc:/qml/images/icon.png"))

	quickcontrols2.QQuickStyle_SetStyle("material")

	engine := qml.NewQQmlApplicationEngine(nil)
	engine.Load(core.NewQUrl3("qrc:/qml/nestmain.qml", 0))

	qmlBridge = NewQmlBridge(nil)

	connectQMLToGOFunctions()

	engine.RootContext().SetContextProperty("QmlBridge", qmlBridge)

	if isPlatformDarwin {
		textLocation := "Your wallet will be saved in your home directory: " + pathToHomeDir + "/"
		qmlBridge.DisplayWalletCreationLocation(textLocation)
	}

	getAndDisplayStartInfoFromDB()

	gui.QGuiApplication_Exec()

	log.Info("Application closed")

	walletdmanager.GracefullyQuitWalletd()
}

func connectQMLToGOFunctions() {

	qmlBridge.ConnectLog(func(msg string) {
		log.Info("QML: ", msg)
	})

	qmlBridge.ConnectClickedButtonCopyAddress(func() {
		clipboard.WriteAll(walletdmanager.WalletAddress)
		qmlBridge.DisplayPopup("Copied!", 1500)
	})

	qmlBridge.ConnectClickedButtonCopyKeys(func() {
		clipboard.WriteAll(stringBackupKeys)
	})

	qmlBridge.ConnectClickedButtonCopyTx(func(transactionID string) {
		clipboard.WriteAll(transactionID)
		qmlBridge.DisplayPopup("Copied!", 1500)
	})

	qmlBridge.ConnectClickedButtonExplorer(func(transactionID string) {
		url := urlBlockExplorer + "?hash=" + transactionID + "#blockchain_transaction"
		successOpenBrowser := openBrowser(url)
		if !successOpenBrowser {
			log.Error("failure opening browser, url: " + url)
		}
	})

	qmlBridge.ConnectClickedButtonSend(func(transferAddress string, transferAmount string, transferPaymentID string, transferFee string, transferMixin string) {
		transfer(transferAddress, transferAmount, transferPaymentID, transferFee, transferMixin)
	})

	qmlBridge.ConnectGetTransferAmountUSD(func(amountTRTL string) string {
		return amountStringUSDToTRTL(amountTRTL)
	})

	qmlBridge.ConnectClickedButtonBackupWallet(func() {
		showWalletPrivateInfo()
	})

	qmlBridge.ConnectClickedButtonOpen(func(pathToWallet string, passwordWallet string) {
		go func() {
			recordPathWalletToDB(pathToWallet)
			startWalletWithWalletInfo(pathToWallet, passwordWallet)
		}()
	})

	qmlBridge.ConnectClickedButtonCreate(func(filenameWallet string, passwordWallet string, confirmPasswordWallet string) {
		go func() {
			createWalletWithWalletInfo(filenameWallet, passwordWallet, confirmPasswordWallet)
		}()
	})

	qmlBridge.ConnectClickedButtonImport(func(filenameWallet string, passwordWallet string, privateViewKey string, privateSpendKey string, confirmPasswordWallet string) {
		go func() {
			importWalletWithWalletInfo(filenameWallet, passwordWallet, confirmPasswordWallet, privateViewKey, privateSpendKey)
		}()
	})

	qmlBridge.ConnectClickedCloseWallet(func() {
		closeWallet()
	})

	qmlBridge.ConnectChoseRemote(func(remote bool) {
		useRemoteNode = remote
		recordUseRemoteToDB(useRemoteNode)
	})

	qmlBridge.ConnectClickedCloseSettings(func() {
		qmlBridge.HideSettingsScreen()
	})

	qmlBridge.ConnectClickedSettingsButton(func() {
		qmlBridge.DisplaySettingsScreen()
	})

	qmlBridge.ConnectChoseDisplayFiat(func(displayFiat bool) {
		displayFiatConversion = displayFiat
		recordDisplayConversionToDB(displayFiat)
	})

	qmlBridge.ConnectSaveRemoteDaemonInfo(func(daemonAddress string, daemonPort string) {
		saveRemoteDaemonInfo(daemonAddress, daemonPort)
	})

	qmlBridge.ConnectResetRemoteDaemonInfo(func() {
		saveRemoteDaemonInfo(defaultRemoteDaemonAddress, defaultRemoteDaemonPort)
		qmlBridge.DisplaySettingsRemoteDaemonInfo(defaultRemoteDaemonAddress, defaultRemoteDaemonPort)
	})

	qmlBridge.ConnectGetFullBalanceAndDisplayInTransferAmount(func(transferFee string) {
		getFullBalanceAndDisplayInTransferAmount(transferFee)
	})

	qmlBridge.ConnectGetDefaultFeeAndMixinAndDisplay(func() {
		getDefaultFeeAndMixinAndDisplay()
	})

	qmlBridge.ConnectLimitDisplayTransactions(func(limit bool) {
		limitDisplayedTransactions = limit
		getAndDisplayListTransactions(true)
	})
}

func startDisplayWalletInfo() {

	getAndDisplayBalances()
	getAndDisplayAddress()
	getAndDisplayListTransactions(true)
	getAndDisplayConnectionInfo()
	getDefaultFeeAndMixinAndDisplay()

	go func() {
		tickerRefreshWalletData = time.NewTicker(time.Second * 30)
		for range tickerRefreshWalletData.C {
			getAndDisplayBalances()
			getAndDisplayListTransactions(false)
		}
	}()

	go func() {
		tickerRefreshConnectionInfo = time.NewTicker(time.Second * 15)
		for range tickerRefreshConnectionInfo.C {
			getAndDisplayConnectionInfo()
		}
	}()
}

func getAndDisplayBalances() {

	walletAvailableBalance, walletLockedBalance, walletTotalBalance, err := walletdmanager.RequestBalance()
	if err == nil {
		qmlBridge.DisplayAvailableBalance(humanize.FormatFloat("#,###.##", walletAvailableBalance))
		qmlBridge.DisplayLockedBalance(humanize.FormatFloat("#,###.##", walletLockedBalance))
		balanceUSD := walletTotalBalance * rateUSDTRTL
		qmlBridge.DisplayTotalBalance(humanize.FormatFloat("#,###.##", walletTotalBalance), humanize.FormatFloat("#,###.##", balanceUSD))
	}
}

func getAndDisplayAddress() {

	walletAddress, err := walletdmanager.RequestAddress()
	if err == nil {
		qmlBridge.DisplayAddress(walletAddress, walletdmanager.WalletFilename, displayFiatConversion)
	}
}

func getAndDisplayConnectionInfo() {

	syncing, blockCount, knownBlockCount, peers, err := walletdmanager.RequestConnectionInfo()
	if err != nil {
		log.Info("error getting connection info: ", err)
		return
	}

	blocks := strconv.Itoa(blockCount) + " / " + strconv.Itoa(knownBlockCount)
	qmlBridge.DisplaySyncingInfo(syncing, blocks, strconv.Itoa(peers))

	// when not connected to remote node, the knownBlockCount stays at 1. So inform users if there seems to be a connection problem
	if useRemoteNode {
		if knownBlockCount == 1 {
			countConnectionProblem++
		} else {
			countConnectionProblem = 0
		}
		if countConnectionProblem > 2 {
			countConnectionProblem = 0
			qmlBridge.DisplayErrorDialog("Error connecting to remote node", "Check your internet connection, the remote node address and the remote node status. If you cannot connect to a remote node, choose the \"local blockchain\" option.")
		}
	}
}

func getAndDisplayListTransactions(forceFullUpdate bool) {

	newTransfers, err := walletdmanager.RequestListTransactions()
	if err == nil {
		needFullUpdate := false
		if len(newTransfers) != len(transfers) || forceFullUpdate {
			needFullUpdate = true
		}
		transfers = newTransfers
		// sort starting by the most recent transaction
		sort.Slice(transfers, func(i, j int) bool { return transfers[i].Timestamp.After(transfers[j].Timestamp) })

		if needFullUpdate {
			transactionNumber := len(transfers)

			qmlBridge.ClearListTransactions()

			for index, transfer := range transfers {
				if limitDisplayedTransactions && index >= numberTransactionsToDisplay {
					break
				}
				amount := transfer.Amount
				amountString := ""
				if amount >= 0 {
					amountString += "+ "
					amountString += strconv.FormatFloat(amount, 'f', -1, 64)
				} else {
					amountString += "- "
					amountString += strconv.FormatFloat(-amount, 'f', -1, 64)
				}
				amountString += " TRTL (fee: " + strconv.FormatFloat(transfer.Fee, 'f', 2, 64) + ")"
				confirmationsString := confirmationsStringRepresentation(transfer.Confirmations)
				timeString := transfer.Timestamp.Format("2006-01-02 15:04:05")
				transactionNumberString := strconv.Itoa(transactionNumber) + ")"
				transactionNumber--

				qmlBridge.AddTransactionToList(transfer.PaymentID, transfer.TxID, amountString, confirmationsString, timeString, transactionNumberString)
			}
		} else { // just update the number of confirmations of transactions with less than 110 conf
			for index, transfer := range transfers {
				if limitDisplayedTransactions && index >= numberTransactionsToDisplay {
					break
				}
				if transfer.Confirmations < 110 {
					qmlBridge.UpdateConfirmationsOfTransaction(index, confirmationsStringRepresentation(transfer.Confirmations))
				} else {
					break
				}
			}
		}
	}
}

func transfer(transferAddress string, transferAmount string, transferPaymentID string, transferFee string, transferMixin string) bool {

	log.Info("SEND: to: ", transferAddress, "  amount: ", transferAmount, "  payment ID: ", transferPaymentID, "  fee: ", transferFee, "  mixin: ", transferMixin)

	transactionID, err := walletdmanager.SendTransaction(transferAddress, transferAmount, transferPaymentID, transferFee, transferMixin)
	if err != nil {
		log.Warn("error transfer: ", err)
		qmlBridge.DisplayErrorDialog("Error transfer.", err.Error())
		return false
	}

	log.Info("succes transfer: ", transactionID)

	getAndDisplayBalances()
	qmlBridge.ClearTransferAmount()
	qmlBridge.DisplayPopup("TRTLs sent successfully", 4000)

	return true
}

func startWalletWithWalletInfo(pathToWallet string, passwordWallet string) bool {

	err := walletdmanager.StartWalletd(pathToWallet, passwordWallet, useRemoteNode, remoteDaemonAddress, remoteDaemonPort)
	if err != nil {
		log.Warn("error starting walletd with provided wallet info. error: ", err)
		qmlBridge.FinishedLoadingWalletd()
		qmlBridge.DisplayErrorDialog("Error opening wallet.", err.Error())
		return false
	}

	log.Info("success starting walletd")

	qmlBridge.FinishedLoadingWalletd()
	startDisplayWalletInfo()
	qmlBridge.DisplayMainWalletScreen()

	return true
}

func createWalletWithWalletInfo(filenameWallet string, passwordWallet string, confirmPasswordWallet string) bool {

	err := walletdmanager.CreateWallet(filenameWallet, passwordWallet, confirmPasswordWallet, "", "")
	if err != nil {
		log.Warn("error creating wallet. error: ", err)
		qmlBridge.FinishedCreatingWallet()
		qmlBridge.DisplayErrorDialog("Error creating the wallet.", err.Error())
		return false
	}

	log.Info("success creating wallet")

	startWalletWithWalletInfo(filenameWallet, passwordWallet)
	showWalletPrivateInfo()

	return true
}

func importWalletWithWalletInfo(filenameWallet string, passwordWallet string, confirmPasswordWallet string, privateViewKey string, privateSpendKey string) bool {

	err := walletdmanager.CreateWallet(filenameWallet, passwordWallet, confirmPasswordWallet, privateViewKey, privateSpendKey)
	if err != nil {
		log.Warn("error importing wallet. error: ", err)
		qmlBridge.FinishedCreatingWallet()
		qmlBridge.DisplayErrorDialog("Error importing the wallet.", err.Error())
		return false
	}

	log.Info("success importing wallet")

	startWalletWithWalletInfo(filenameWallet, passwordWallet)

	return true
}

func closeWallet() {

	tickerRefreshWalletData.Stop()
	tickerRefreshConnectionInfo.Stop()

	stringBackupKeys = ""
	transfers = nil
	limitDisplayedTransactions = true
	countConnectionProblem = 0

	go func() {
		walletdmanager.GracefullyQuitWalletd()
	}()

	qmlBridge.DisplayOpenWalletScreen()
}

func showWalletPrivateInfo() {

	privateViewKey, privateSpendKey, err := walletdmanager.GetPrivateViewKeyAndSpendKey()
	if err != nil {
		log.Error("Error getting view and spend key: ", err)
	} else {
		qmlBridge.DisplayPrivateKeys(walletdmanager.WalletFilename, privateViewKey, privateSpendKey, walletdmanager.WalletAddress)

		stringBackupKeys = "Wallet: " + walletdmanager.WalletFilename + "\nAddress: " + walletdmanager.WalletAddress + "\nPrivate view key: " + privateViewKey + "\nPrivate spend key: " + privateSpendKey
	}
}

func getFullBalanceAndDisplayInTransferAmount(transferFee string) {

	availableBalance, err := walletdmanager.RequestAvailableBalanceToBeSpent(transferFee)
	if err != nil {
		qmlBridge.DisplayErrorDialog("Error calculating full balance minus fee.", err.Error())
	}
	qmlBridge.DisplayFullBalanceInTransferAmount(humanize.FtoaWithDigits(availableBalance, 2))
}

func getDefaultFeeAndMixinAndDisplay() {

	qmlBridge.DisplayDefaultFeeAndMixin(humanize.FtoaWithDigits(walletdmanager.DefaultTransferFee, 2), humanize.FormatInteger("#.", walletdmanager.DefaultTransferMixin))
}

func saveRemoteDaemonInfo(daemonAddress string, daemonPort string) {

	remoteDaemonAddress = daemonAddress
	remoteDaemonPort = daemonPort
	recordRemoteDaemonInfoToDB(remoteDaemonAddress, remoteDaemonPort)
	remoteNodeDescr := "Remote node (" + remoteDaemonAddress + ")"
	qmlBridge.DisplayUseRemoteNode(getUseRemoteFromDB(), remoteNodeDescr)
}

func setupDB(pathToDB string) {

	var err error
	db, err = sql.Open("sqlite3", pathToDB)
	if err != nil {
		log.Fatal("error opening db file. err: ", err)
	}

	_, err = db.Exec("CREATE TABLE IF NOT EXISTS pathWallet (id INTEGER PRIMARY KEY AUTOINCREMENT,path VARCHAR(64) NULL)")
	if err != nil {
		log.Fatal("error creating table pathWallet. err: ", err)
	}

	_, err = db.Exec("CREATE TABLE IF NOT EXISTS remoteNode (id INTEGER PRIMARY KEY AUTOINCREMENT, useRemote BOOL NOT NULL DEFAULT '1')")
	if err != nil {
		log.Fatal("error creating table remoteNode. err: ", err)
	}

	_, err = db.Exec("CREATE TABLE IF NOT EXISTS fiatConversion (id INTEGER PRIMARY KEY AUTOINCREMENT, displayFiat BOOL NOT NULL DEFAULT '0', currency VARCHAR(64) DEFAULT 'USD')")
	if err != nil {
		log.Fatal("error creating table fiatConversion. err: ", err)
	}

	_, err = db.Exec("CREATE TABLE IF NOT EXISTS remoteNodeInfo (id INTEGER PRIMARY KEY AUTOINCREMENT, address VARCHAR(64), port VARCHAR(64))")
	if err != nil {
		log.Fatal("error creating table remoteNodeInfo. err: ", err)
	}
}

func getAndDisplayStartInfoFromDB() {

	qmlBridge.DisplayPathToPreviousWallet(getPathWalletFromDB())
	remoteDaemonAddress, remoteDaemonPort = getRemoteDaemonInfoFromDB()
	remoteNodeDescr := "Remote node (" + remoteDaemonAddress + ")"
	qmlBridge.DisplayUseRemoteNode(getUseRemoteFromDB(), remoteNodeDescr)
	qmlBridge.DisplaySettingsValues(getDisplayConversionFromDB())
	qmlBridge.DisplaySettingsRemoteDaemonInfo(remoteDaemonAddress, remoteDaemonPort)
}

func getPathWalletFromDB() string {

	pathToPreviousWallet := ""

	rows, err := db.Query("SELECT path FROM pathWallet ORDER BY id DESC LIMIT 1")
	if err != nil {
		log.Fatal("error querying path from pathwallet table. err: ", err)
	}
	defer rows.Close()
	for rows.Next() {
		path := ""
		err = rows.Scan(&path)
		if err != nil {
			log.Fatal("error reading item from pathWallet table. err: ", err)
		}
		pathToPreviousWallet = path
	}

	return pathToPreviousWallet
}

func recordPathWalletToDB(path string) {

	stmt, err := db.Prepare(`INSERT INTO pathWallet(path) VALUES(?)`)
	if err != nil {
		log.Fatal("error preparing to insert pathWallet into db. err: ", err)
	}
	_, err = stmt.Exec(path)
	if err != nil {
		log.Fatal("error inserting pathWallet into db. err: ", err)
	}
}

func getUseRemoteFromDB() bool {

	rows, err := db.Query("SELECT useRemote FROM remoteNode ORDER BY id DESC LIMIT 1")
	if err != nil {
		log.Fatal("error querying useRemote from remoteNode table. err: ", err)
	}
	defer rows.Close()
	for rows.Next() {
		useRemote := true
		err = rows.Scan(&useRemote)
		if err != nil {
			log.Fatal("error reading item from remoteNode table. err: ", err)
		}
		useRemoteNode = useRemote
	}

	return useRemoteNode
}

func recordUseRemoteToDB(useRemote bool) {

	stmt, err := db.Prepare(`INSERT INTO remoteNode(useRemote) VALUES(?)`)
	if err != nil {
		log.Fatal("error preparing to insert useRemoteNode into db. err: ", err)
	}
	_, err = stmt.Exec(useRemote)
	if err != nil {
		log.Fatal("error inserting useRemoteNode into db. err: ", err)
	}
}

func getRemoteDaemonInfoFromDB() (daemonAddress string, daemonPort string) {

	rows, err := db.Query("SELECT address, port FROM remoteNodeInfo ORDER BY id DESC LIMIT 1")
	if err != nil {
		log.Fatal("error querying address and port from remoteNodeInfo table. err: ", err)
	}
	defer rows.Close()
	for rows.Next() {
		daemonAddress := ""
		daemonPort := ""
		err = rows.Scan(&daemonAddress, &daemonPort)
		if err != nil {
			log.Fatal("error reading item from remoteNodeInfo table. err: ", err)
		}
		remoteDaemonAddress = daemonAddress
		remoteDaemonPort = daemonPort
	}

	return remoteDaemonAddress, remoteDaemonPort
}

func recordRemoteDaemonInfoToDB(daemonAddress string, daemonPort string) {

	stmt, err := db.Prepare(`INSERT INTO remoteNodeInfo(address,port) VALUES(?,?)`)
	if err != nil {
		log.Fatal("error preparing to insert address and port of remote node into db. err: ", err)
	}
	_, err = stmt.Exec(daemonAddress, daemonPort)
	if err != nil {
		log.Fatal("error inserting address and port of remote node into db. err: ", err)
	}
}

func getDisplayConversionFromDB() bool {

	rows, err := db.Query("SELECT displayFiat FROM fiatConversion ORDER BY id DESC LIMIT 1")
	if err != nil {
		log.Fatal("error reading displayFiat from fiatConversion table. err: ", err)
	}
	defer rows.Close()
	for rows.Next() {
		displayFiat := false
		err = rows.Scan(&displayFiat)
		if err != nil {
			log.Fatal("error reading item from fiatConversion table. err: ", err)
		}
		displayFiatConversion = displayFiat
	}

	return displayFiatConversion
}

func recordDisplayConversionToDB(displayConversion bool) {

	stmt, err := db.Prepare(`INSERT INTO fiatConversion(displayFiat) VALUES(?)`)
	if err != nil {
		log.Fatal("error preparing to insert displayFiat into db. err: ", err)
	}
	_, err = stmt.Exec(displayConversion)
	if err != nil {
		log.Fatal("error inserting displayFiat into db. err: ", err)
	}
}

func openBrowser(url string) bool {
	var args []string
	switch runtime.GOOS {
	case "darwin":
		args = []string{"open"}
	case "windows":
		args = []string{"cmd", "/c", "start"}
	default:
		args = []string{"xdg-open"}
	}
	cmd := exec.Command(args[0], append(args[1:], url)...)
	return cmd.Start() == nil
}

func requestRateTRTL() {
	response, err := http.Get(urlCryptoCompareTRTL)

	if err != nil {
		log.Error("error fetching from cryptocompare: ", err)
	} else {
		b, err := ioutil.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			log.Error("error reading result from cryptocompare: ", err)
		} else {
			var resultInterface interface{}
			if err := json.Unmarshal(b, &resultInterface); err != nil {
				log.Error("error JSON unmarshaling request cryptocompare: ", err)
			} else {
				resultsMap := resultInterface.(map[string]interface{})
				rateUSDTRTL = resultsMap["USD"].(float64)
			}
		}
	}
}

func amountStringUSDToTRTL(amountTRTLString string) string {
	amountTRTL, err := strconv.ParseFloat(amountTRTLString, 64)
	if err != nil || amountTRTL <= 0 || rateUSDTRTL == 0 {
		return ""
	}
	amountUSD := amountTRTL * rateUSDTRTL
	amountUSDString := strconv.FormatFloat(amountUSD, 'f', 2, 64) + " $"
	return amountUSDString
}

func confirmationsStringRepresentation(confirmations int) string {
	confirmationsString := "("
	if confirmations > 100 {
		confirmationsString += ">100"
	} else {
		confirmationsString += strconv.Itoa(confirmations)
	}
	confirmationsString += " conf.)"
	return confirmationsString
}
